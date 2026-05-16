package agent

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/swl"
)

// SWLHook implements ToolInterceptor, LLMInterceptor, and RuntimeEventObserver.
// One instance is mounted per agent via mountAgentSWLHooks().
type SWLHook struct {
	manager *swl.Manager
	agentID string
	wg      sync.WaitGroup
}

// Compile-time interface checks.
var (
	_ ToolInterceptor      = (*SWLHook)(nil)
	_ LLMInterceptor       = (*SWLHook)(nil)
	_ RuntimeEventObserver = (*SWLHook)(nil)
)

// Close drains all pending async goroutines.
func (h *SWLHook) Close() error {
	h.wg.Wait()
	return nil
}

// --- RuntimeEventObserver ---

func (h *SWLHook) OnRuntimeEvent(ctx context.Context, evt runtimeevents.Event) error {
	if !h.matchesAgent(evt.Scope.AgentID) {
		return nil
	}

	switch evt.Kind {
	case runtimeevents.KindAgentTurnStart:
		payload, ok := evt.Payload.(TurnStartPayload)
		if !ok || payload.UserMessage == "" {
			return nil
		}
		sessionKey := evt.Scope.SessionKey
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer recoverSWLHook("OnRuntimeEvent/TurnStart")
			sessionID := h.manager.EnsureSession(sessionKey)
			h.manager.SetSessionGoal(sessionID, truncateSWL(payload.UserMessage, 200))

			if payload.UserMessage != "" {
				intentID := swl.EntityIDFor(swl.KnownTypeIntent, sessionID+":"+truncateSWL(payload.UserMessage, 50))
				_ = h.manager.UpsertEntity(swl.EntityTuple{
					ID: intentID, Type: swl.KnownTypeIntent,
					Name:             truncateSWL(payload.UserMessage, 120),
					Confidence:       1.0,
					ExtractionMethod: swl.MethodObserved,
					KnowledgeDepth:   1,
				})
				_ = h.manager.UpsertEdge(swl.EdgeTuple{
					FromID: sessionID, Rel: swl.KnownRelIntendedFor, ToID: intentID, SessionID: sessionID,
				})
			}
		}()

	case runtimeevents.KindAgentSubTurnSpawn:
		payload, ok := evt.Payload.(SubTurnSpawnPayload)
		if !ok {
			return nil
		}
		sessionKey := evt.Scope.SessionKey
		h.wg.Add(1)
		go func() {
			defer h.wg.Done()
			defer recoverSWLHook("OnRuntimeEvent/SubTurnSpawn")
			sessionID := h.manager.EnsureSession(sessionKey)
			subID := swl.EntityIDFor(swl.KnownTypeSubAgent, payload.AgentID+":"+payload.Label)
			_ = h.manager.UpsertEntity(swl.EntityTuple{
				ID: subID, Type: swl.KnownTypeSubAgent,
				Name:             fmt.Sprintf("%s/%s", payload.AgentID, payload.Label),
				Confidence:       1.0,
				ExtractionMethod: swl.MethodObserved,
				KnowledgeDepth:   1,
			})
			_ = h.manager.UpsertEdge(swl.EdgeTuple{
				FromID: subID, Rel: swl.KnownRelSpawnedBy, ToID: sessionID, SessionID: sessionID,
			})
		}()
	}

	return nil
}

// --- LLMInterceptor ---

func (h *SWLHook) BeforeLLM(ctx context.Context, req *LLMHookRequest) (*LLMHookRequest, HookDecision, error) {
	return req, HookDecision{Action: HookActionContinue}, nil
}

func (h *SWLHook) AfterLLM(ctx context.Context, resp *LLMHookResponse) (*LLMHookResponse, HookDecision, error) {
	if !h.matchesAgent(resp.Meta.AgentID) || resp.Response == nil {
		return resp, HookDecision{Action: HookActionContinue}, nil
	}

	content := resp.Response.Content
	reasoning := resp.Response.ReasoningContent
	sessionKey := resp.Meta.SessionKey

	if content == "" && reasoning == "" {
		return resp, HookDecision{Action: HookActionContinue}, nil
	}

	modelID := resp.Model

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer recoverSWLHook("AfterLLM")
		sessionID := h.manager.EnsureSession(sessionKey)
		h.manager.SetSessionModel(sessionID, modelID)

		if content != "" {
			delta := h.manager.ExtractLLMResponse(sessionID, content)
			if delta != nil && !delta.IsEmpty() {
				_ = h.manager.ApplyDelta(delta, sessionID)
			}
		}
		if reasoning != "" {
			delta := h.manager.ExtractLLMResponse(sessionID, reasoning)
			if delta != nil && !delta.IsEmpty() {
				confCap := h.manager.Config().EffectiveReasoningConfidenceCap()
				for i := range delta.Entities {
					if delta.Entities[i].Confidence > confCap {
						delta.Entities[i].Confidence = confCap
					}
					delta.Entities[i].ExtractionMethod = swl.MethodInferred
				}
				_ = h.manager.ApplyDelta(delta, sessionID)
			}
		}
	}()

	return resp, HookDecision{Action: HookActionContinue}, nil
}

// --- ToolInterceptor ---

func (h *SWLHook) BeforeTool(
	ctx context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	if !h.matchesAgent(call.Meta.AgentID) {
		return call, HookDecision{Action: HookActionContinue}, nil
	}
	shouldBlock, reason := h.manager.PreHook(call.Tool, call.Arguments)
	if shouldBlock {
		return call, HookDecision{Action: HookActionDenyTool, Reason: reason}, nil
	}
	return call, HookDecision{Action: HookActionContinue}, nil
}

func (h *SWLHook) AfterTool(
	ctx context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	if !h.matchesAgent(result.Meta.AgentID) {
		return result, HookDecision{Action: HookActionContinue}, nil
	}

	toolName := result.Tool
	args := result.Arguments
	sessionKey := result.Meta.SessionKey
	var toolResult string
	if result.Result != nil {
		toolResult = result.Result.ForLLM
	}

	// Check for staleness hint before PostHook marks the entity as verified.
	_, hint := h.manager.PreHook(toolName, args)
	if hint != "" && result.Result != nil {
		result = result.Clone()
		result.Result.ForLLM = "[SWL] " + hint + "\n\n" + result.Result.ForLLM
	}

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer recoverSWLHook("AfterTool/" + toolName)
		h.manager.PostHook(sessionKey, toolName, args, toolResult)
	}()

	return result, HookDecision{Action: HookActionContinue}, nil
}

func (h *SWLHook) matchesAgent(agentID string) bool {
	return h.agentID == "" || h.agentID == agentID
}

func recoverSWLHook(label string) {
	if r := recover(); r != nil {
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		logger.WarnCF("swl", fmt.Sprintf("panic in %s: %v", label, r), map[string]any{
			"stack": string(buf[:n]),
		})
	}
}

func truncateSWL(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
