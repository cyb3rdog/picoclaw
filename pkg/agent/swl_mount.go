package agent

import (
	"github.com/sipeed/picoclaw/pkg/logger"
)

// mountAgentSWLHooks mounts a SWLHook into the HookManager for each agent
// that has SWL enabled. Called once at Run() startup.
func (al *AgentLoop) mountAgentSWLHooks() {
	if al.registry == nil {
		return
	}
	for _, agentID := range al.registry.ListAgentIDs() {
		inst, ok := al.registry.GetAgent(agentID)
		if !ok || inst.SWLManager == nil {
			continue
		}
		hookName := "swl_" + agentID
		if err := al.MountHook(HookRegistration{
			Name:     hookName,
			Source:   HookSourceInProcess,
			Priority: 10,
			Hook:     &SWLHook{manager: inst.SWLManager, agentID: agentID},
		}); err != nil {
			logger.WarnCF("agent", "Failed to mount SWL hook",
				map[string]any{"agent_id": agentID, "error": err.Error()})
		}
	}
}
