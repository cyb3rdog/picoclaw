# PicoClaw — CLAUDE.md

Orientation guide for AI assistants working in this codebase.

---

## Project Overview

PicoClaw is an ultra-lightweight AI assistant written entirely in Go. It targets constrained hardware (Raspberry Pi Zero 2W, $10 RISC-V boards) while supporting 30+ LLM providers and 19+ chat channels.

- **Module:** `github.com/sipeed/picoclaw`
- **Go version:** 1.25+
- **Key targets:** <10 MB RAM, <1s cold-start on 0.8 GHz CPUs
- **License:** MIT
- **Maintained by:** Sipeed

---

## Agentic Workflow

These rules govern how AI assistants should approach all work in this codebase. They apply regardless of task size and are designed to be followed by any capable agent.

### Principles

1. **Understand before act** — Never edit code you have not read. State your understanding before changing anything.
2. **Plan before implement** — Write a numbered plan with file paths and expected outcomes. Get alignment. Then build.
3. **Minimum context, maximum precision** — Read the least necessary to act with certainty. Expand only when blocked.
4. **Verify, don't assume** — If something is unknown, research it or ask. Never infer behavior from names or structure alone.
5. **One active task** — Track work with a todo list. Only one item in progress at a time.
6. **Quality gates are not optional** — Tests, lint, and build pass before any step is called done.

### Context Economy

Context is finite and expensive. Read the minimum necessary to act with certainty.

- **Search before read.** Use `grep` or `find` to locate before using Read to understand. Never open a file to discover where something is defined.
- **Read ranges, not whole files.** When you know where the relevant code is, use `offset` + `limit`. A 2000-line file does not need to be fully read to understand one function.
- **Never re-read.** If a file has been read this session, use what is already known.
- **Delegate large-output tasks to subagents.** Any task producing hundreds of lines of output belongs in a subagent. The agent returns a summary; raw output never enters the main context.
- **Skip generated and vendored files.** Never read `go.sum`, `pnpm-lock.yaml`, `*.pb.go`, `dist/`, `vendor/`, or any auto-generated file unless the task is specifically about them.
- **Stop when the gap is filled.** Before searching, state the specific unknown being resolved. Stop the moment it is answered.

### Development Loop

```
EXPLORE → PLAN → REFINE → IMPLEMENT → VALIDATE → REFLECT
```

| Phase | What happens | Exit condition |
|-------|-------------|---------------|
| **Explore** | Targeted research. Grep first. Read only what's needed. State unknowns explicitly. | No unresolved unknowns in scope. |
| **Plan** | Write a numbered plan: action + target file(s) + expected outcome per step. | Plan is complete and written down. |
| **Refine** | Present the plan. Resolve disagreements before writing a single line of code. | User aligned, or open questions answered. |
| **Implement** | Execute step by step. One todo active at a time. No scope creep beyond the plan. | All steps done, no partial work. |
| **Validate** | Run the full quality gate suite. Fix every failure before moving on. | `make check` passes. |
| **Reflect** | "What's missing?" audit — edge cases, error paths, docs, regressions. | Nothing outstanding, or a new loop is started. |

Skipping **Explore** leads to wrong assumptions. Skipping **Plan** causes scope creep. Skipping **Validate** ships broken code. Skipping **Reflect** leaves tasks half-done.

### Planning Rules

- Number every step. State: what to do, which file(s), and the expected outcome.
- Flag unknowns before finalising: *"Unknown: whether X — will verify before step N."* A plan with unresolved unknowns is a draft, not a plan.
- If a plan exceeds ~15 steps, the task needs splitting into a series of smaller loops.
- Never present a plan as ready if gaps remain. Research first.

### Specialist Agent Dispatch

Use subagents to parallelize independent work and keep the main context clean.

| Task | Agent type | Notes |
|------|-----------|-------|
| Locating files, symbols, or patterns | `Explore` | Fastest; excerpt-based; does not flood main context |
| Architecture or design trade-offs | `Plan` | Returns a structured plan only; no implementation |
| Deep multi-file codebase research | `Explore` (thorough) | Give each agent a distinct, specific search focus |
| Self-contained implementation or refactor | `general-purpose` | Prompt must be fully self-contained with all context |

**When dispatching agents:**
- Provide: relevant file paths, what is already known, and the exact deliverable expected.
- Do not spawn an agent for work that takes fewer than 3 targeted reads — do it directly.
- Launch independent agents in parallel (single message, multiple calls) when their inputs do not depend on each other.
- Synthesize results yourself. Do not chain agents without verifying each intermediate output.

### Quality Gates

**Before implementation starts:**
- [ ] Relevant source files have been read (not just grepped)
- [ ] Existing patterns being extended are understood
- [ ] Plan is written, numbered, and user-aligned
- [ ] No unresolved unknowns remain in scope

**Before marking each step done:**
- [ ] Code compiles
- [ ] Affected tests pass
- [ ] No new lint errors introduced

**Before closing any task:**
- [ ] `make check` passes (full suite: deps + fmt + vet + test + lint-docs)
- [ ] New behaviour is covered by tests, or existing tests are updated
- [ ] Docs updated if public APIs, config schema, or CLI behaviour changed
- [ ] "What's missing?" audit: edge cases, error paths, regressions checked

### Anti-patterns

- Edit a file without having read it first
- Fix a symptom without understanding the root cause
- Assume a function is unused without grepping its callsites
- Add abstractions, helpers, or cleanup not required by the stated task
- Skip `make check` for changes that seem small
- Mark a todo complete when tests are failing or work is partial
- Follow a rabbit hole deeper without surfacing it to the user first
- Use `--no-verify`, force-push, or bypass any hook to unblock yourself

---

## Repository Layout

```
cmd/
  picoclaw/           CLI entry point (Cobra); subcommands in internal/
    internal/
      agent/          Interactive chat and one-shot queries
      auth/           Authentication flows
      cron/           Scheduled task management
      gateway/        WebSocket/HTTP server commands
      mcp/            Model Context Protocol CLI
      migrate/         Data migration
      model/          Model selection
      onboard/        First-run wizard
      skills/         Skill discovery and install
      status/         Agent status
      version/        Version info
  membench/           Memory benchmarking tool

pkg/                  Core library (37 packages)
  agent/              Agent loop, turn coordination, context management
  channels/           18+ messaging platform integrations
  providers/          30+ LLM provider implementations
  config/             Configuration structs and parsing
  swl/                Semantic Workspace Layer (see SWL section)
  tools/              Tool registry and built-in tools (fs/, hardware/, integration/)
  memory/             JSONL-backed conversation history
  session/            Session store interface and management
  bus/                Central MessageBus for inter-component communication
  mcp/                Model Context Protocol server coordination
  routing/            Smart model routing (light vs heavy queries)
  gateway/            HTTP webhook receiver for channels
  skills/             Modular capability extensions
  auth/               Authentication handling
  credential/         Credential storage
  cron/               Scheduled task execution
  logger/             zerolog-based structured logging
  tokenizer/          Token counting for LLM cost tracking
  media/ audio/       Media and audio processing
  identity/ device/   Identity and device detection
  events/ bus/        Runtime event system
  isolation/          Filesystem and context sandboxing
  migrate/            Data migration framework
  updater/            Self-update mechanism
  utils/ fileutil/    Utility helpers

web/
  backend/            Go REST API and WebSocket server for WebUI launcher
  frontend/           React/TypeScript web interface (pnpm, Vite, TanStack Router)

docs/
  swl/                SWL design documents (SWL-DESIGN.md, SWL-PHASE-B-SPEC.md, etc.)
  channels/           Per-channel setup guides
  guides/             Deployment, Docker, hardware, provider guides
  reference/          Cron, MCP CLI, tools reference
  architecture/       Hooks, steering, subturn design docs

config/               config.example.json — full configuration template
docker/               Multi-variant Dockerfiles and docker-compose files
scripts/              build-macos-app.sh, lint-docs.sh, test-docker-mcp.sh
workspace/            Runtime workspace templates
.github/workflows/    CI/CD pipelines
```

---

## Build & Development

All commands run from the repository root. The Makefile is the canonical build interface.

```bash
# Core builds
make build            # Build for current platform (runs go generate first)
make build-all        # Cross-compile for all platforms
make build-launcher   # Build WebUI launcher binary

# Code generation
make generate         # Run go generate

# Install / uninstall
make install          # Install to ~/.local/bin
make uninstall        # Remove binary or all data

# Quality checks — run before every commit
make check            # Full pre-commit: deps + fmt + vet + test + lint-docs
make fmt              # Format code (gofmt/gofumpt/goimports/golines)
make vet              # go vet static analysis
make lint             # Full golangci-lint run
make lint-docs        # Check docs layout and naming consistency

# Testing
make test             # Run all tests

# Dependency management
make deps             # Tidy and download dependencies
make update-deps      # Update all dependencies

# Docker
make docker-build     # Minimal Alpine image
make docker-build-full # Full-featured image with Node.js
make docker-run       # Run via docker-compose

# Benchmarks
make mem              # Memory benchmark using LOCOMO dataset
```

**Default build tags:** `goolm,stdjson`  
**CGO:** disabled by default (`CGO_ENABLED=0`); SQLite uses `modernc.org/sqlite` (pure Go)  
**Version:** injected via ldflags from `git describe --tags --always --dirty`

### Single test / benchmark

```bash
go test -run TestName -v ./pkg/session/
go test -bench=. -benchmem -run='^$' ./...
```

---

## Code Style

| Rule | Value |
|------|-------|
| Formatter | `gofmt`, `gofumpt`, `goimports`, `golines` |
| Max line length | 120 characters |
| Import order | standard → third-party → localmodule (`gci`) |
| `interface{}` | Replace with `any` (gofmt rewrite rule enforces this) |
| Spelling locale | US English |
| Function length | Target ≤120 lines / ≤40 statements (not yet enforced in CI) |
| Max return values | 3 (revive warning) |

Run `make check` before every commit. All CI checks (lint, vet, test, lint-docs) must pass.

### Testing conventions

- Test files colocated with source: `*_test.go` in same directory
- Integration tests: `*_integration_test.go` suffix
- Assertions: `github.com/stretchr/testify`
- `funlen`, `gocognit`, `gocyclo` rules are exempt in test files

---

## Key Architecture

### Agent system (`pkg/agent/`)

- `instance.go` — `AgentInstance`: ID, Provider, Sessions, Tools, Router, SWL manager reference
- `pipeline.go` — `Pipeline`: Bus, Config, ContextManager, Hooks, FallbackChain, ChannelManager
- Turn-based execution loop; hooks operate as observer, interceptor, or approval gating
- `swl_hook.go` — SWL hook (implements ToolInterceptor, LLMInterceptor, RuntimeEventObserver)
- `swl_mount.go` — mounts one SWLHook per agent at priority 10

### Provider system (`pkg/providers/`)

```go
type LLMProvider interface {
    Chat(ctx, messages, tools, model, options) (*LLMResponse, error)
    GetDefaultModel() string
}
type StreamingProvider interface {
    ChatStream(ctx, messages, tools, model, options, onChunk) (*LLMResponse, error)
}
type StatefulProvider interface {
    LLMProvider
    Close()
}
```

Factory pattern for selection; `FallbackChain` for redundancy.  
Implementations: `anthropic/`, `openai_compat/`, `azure/`, `bedrock/`, `oauth/`, `cli/`, `httpapi/`

### Channel system (`pkg/channels/`)

Base `Channel` interface plus capability interfaces: `TypingCapable`, `StreamingCapable`, `MessageEditor`.  
18+ implementations: telegram, discord, slack, weixin, wecom, qq, matrix, dingtalk, feishu, line, whatsapp, vk, irc, onebot, mqtt, maixcam, pico, teams_webhook.

### Config (`pkg/config/`)

Central `Config` struct: `AgentsConfig`, `ChannelsConfig`, `SecureModelList`, `GatewayConfig`, `ToolsConfig`, `HooksConfig`, `EventsConfig`.  
See `config/config.example.json` for the full annotated template.

### Message bus (`pkg/bus/`)

`MessageBus` is the single internal communication backbone.  
Message types: `InboundMessage`, `OutboundMessage`, `AudioChunk`, `VoiceControl`.

---

## SWL — Semantic Workspace Layer

SWL is the persistent, self-improving semantic knowledge store for agents operating in a workspace. It extracts and retains verified facts about the workspace so agents do not re-discover structure on every request.

**Source:** `pkg/swl/` | **Docs:** `docs/swl/`  
**Config struct:** `pkg/config/swl.go` → `SWLToolConfig`  
**Agent wiring:** `pkg/agent/swl_hook.go`, `pkg/agent/swl_mount.go`  
**Web API:** `web/backend/api/swl.go`  
**Frontend:** `web/frontend/src/components/swl/`, `web/frontend/src/routes/swl.tsx`

### Design principles

- **Generic over specific** — works for Go, Python, firmware, research repos, configs, etc.
- **Cheap before expensive** — Tier 0-2 extraction costs near-zero; Tier 3 LLM indexing is opt-in
- **LLM-agnostic** — self-improvement is grounded in workspace actions, not LLM behavior
- **Multi-agent neutral** — shared semantic layer; convergence equals verified knowledge
- **Additive evolution** — all public APIs preserved across refactoring phases
- **Bounded by design** — ~100-300 entities regardless of workspace size

### Extraction tiers

| Tier | Name | Cost | Default | Trigger |
|------|------|------|---------|---------|
| 0 | Structural/derivative | Near-zero | Always | Scan time |
| 1 | Ontological inference | Cheap (SQL) | Always | Scan + after writes |
| 2 | Passive LLM capture | Free | Always | `AfterLLM` tool hook |
| 3 | Active LLM indexing | Expensive | Off | Scan only, opt-in |

### Key types (`pkg/swl/types.go`)

```go
// EntityType constants
File, Directory, Symbol, Dependency, Task, Section, Topic, URL,
Commit, Session, Note, Command, Intent, SubAgent, Tool,
AnchorDocument, SemanticArea

// EdgeRel constants
defines, imports, has_task, has_section, mentions, depends_on, tagged,
in_dir, written_in, edited_in, appended_in, read, fetched, executed,
deleted, describes, committed_in, found, listed, spawned_by,
context_of, reasoned, intended_for, uses, documents, has_area,
co_occurs_with  // auto-derived: entities co-occurring in ≥4 sessions

// FactStatus
unknown, verified, stale, deleted

// ExtractionMethod (confidence: observed 1.0, stated 0.85, extracted 0.9, inferred 0.8)
observed, stated, extracted, inferred
```

Key write unit types: `EntityTuple`, `EdgeTuple`, `GraphDelta`, `LabelResult`.

### Upsert invariants (never violate these)

- Confidence **never decreases** — `MAX(existing, new)`
- `knowledge_depth` **never decreases** — `MAX(existing, new)`
- `extraction_method` follows priority: `observed > stated > extracted > inferred`
- `fact_status` can **only** be changed via `Manager.SetFactStatus()`

### Manager public API (`pkg/swl/manager.go`)

```go
func NewManager(workspace string, cfg *Config) (*Manager, error)
func (m *Manager) UpsertEntity(e EntityTuple) error
func (m *Manager) UpsertEdge(e EdgeTuple) error
func (m *Manager) ApplyDelta(delta *GraphDelta, sessionID string) error
func (m *Manager) SetFactStatus(entityID string, status FactStatus) error
func (m *Manager) SetSessionModel(sessionID, modelID string)           // per-model reliability tracking
func (m *Manager) ScanWorkspace(root string, sessionKey ...string) (ScanStats, error)
func (m *Manager) Ask(question string) string
func (m *Manager) BumpAccessCount(ids []string)
func (m *Manager) DeriveLabels(entityType EntityType, name string) LabelResult
func (m *Manager) BuildSnapshot(root string) *GraphDelta
func (m *Manager) EnsureSession(sessionKey string) string
func (m *Manager) SetSessionGoal(sessionID, goal string) error
func (m *Manager) ExtractLLMResponse(sessionID, content string) *GraphDelta
func (m *Manager) PreHook(tool string, args map[string]any) (bool, string)
func (m *Manager) PostHook(sessionKey, tool string, args map[string]any, result string)
func (m *Manager) KnowledgeGaps() string
func (m *Manager) Stats() string
func (m *Manager) Schema() string
func (m *Manager) Close() error
```

### Query tiers

| Tier | Mechanism | Latency |
|------|-----------|---------|
| 1 | 30+ YAML intent patterns → handler dispatch | <10 ms |
| 2 | SQL templates for structural queries | <50 ms |
| 3 | Freetext name+metadata search (capped 3 terms) | <100 ms |
| Fallthrough | Records query gap after 3 failures | — |

### Semantic labels (`pkg/swl/labels.go`)

Labels derived at scan time from path prefix, name pattern, and extension rules:

```go
type LabelResult struct {
    Role        string  // "authentication", "api", "logging"
    Domain      string  // "security", "networking", "data-access"
    Kind        string  // "entry-point", "test", "mock", "configuration"
    ContentType string  // "sql", "hcl", "protobuf", "markdown"
    Visibility  string  // "internal", "public"
}
```

### Rules and query configuration

- Embedded defaults: `pkg/swl/swl.rules.default.yaml`, `pkg/swl/swl.query.default.yaml`
- Workspace overrides: `{workspace}/.swl/swl.rules.yaml`, `{workspace}/.swl/swl.query.yaml`
- Deep-merge at load time; no behavioral change without explicit overrides

### Extraction limits (defaults)

| Entity | Limit per file |
|--------|---------------|
| Symbols | 60 |
| Imports | 40 |
| Tasks | 30 |
| Sections | 20 |
| URLs | 20 |
| Max file size | 512 KB |
| Context timeout | 2s per extractor |

### Ignore patterns

Default ignored dirs: `.git`, `node_modules`, `.svn`, `.hg`, `vendor`, `deps`, `.idea`, `.vscode`, `__pycache__`, `.pytest_cache`, `dist`, `build`, `target`, `.cache`  
Default ignored extensions: binary and media types (`.png`, `.jpg`, `.pdf`, `.zip`, `.exe`, `.so`, `.pyc`, etc.)  
Workspace override: `{workspace}/.swlignore`

### Agent hook lifecycle

```
TurnStart     → create Intent entity, edge to session
SubTurnSpawn  → create SubAgent entity, spawned_by edge
AfterLLM      → ExtractLLMResponse async (confidence capped at 0.75 for reasoning)
BeforeTool    → PreHook (may block; query_swl gating)
AfterTool     → PostHook async (file content extraction, symbol/import/task capture)
```

### SQLite database schema

```
entities:    id, type, name, metadata (JSONB), confidence, knowledge_depth,
             extraction_method, fact_status, content_hash,
             created_at, modified_at, accessed_at, access_count
edges:       from_id, rel, to_id (composite PK), source_session, confirmed_at
sessions:    id, key, started_at, ended_at, goal
query_gaps:  id, question, attempt_count, last_attempt_at, suggestion
```

### Phase completion status

| Phase | Description | Status |
|-------|-------------|--------|
| A | BuildSnapshot, lazy extraction, events, access_count fix, gap recording | Done |
| A.2 | Path pattern → semantic label derivation (Tier 1 inference) | Done |
| A.3 | labelSearch, 30+ Tier 1 intent patterns, label-weighted scoring | Done |
| B | Externalize extraction + query logic to YAML rules files | Done |
| C | Autonomous feedback loop, gap → candidate rule generation, inline suggestions | Done |
| D | SSE edge delivery, assertion-in-metadata, Intent/SubAgent wiring | Done |
| N+1 | Graph provenance (context_of), evidence-based decay, DeriveAreaRelations, DeriveSymbolUsage, per-model reliability (ADDENDUM 1) | Done |

### Agent hook lifecycle

```
TurnStart     → create Intent entity, edge to session; update sessions.goal
SubTurnSpawn  → create SubAgent entity, spawned_by edge
AfterLLM      → SetSessionModel(sessionID, resp.Model); ExtractLLMResponse async
                (extracts Tasks/URLs/Files + wires context_of edges to session)
BeforeTool    → PreHook (may block; query_swl gating, stale file notice)
AfterTool     → PostHook async (file content extraction, symbol/import/task capture,
                recordToolEvent with model_id)
```

### Known non-bug behaviors

- SWL source files show "unknown" fact_status: extraction is lazy (on tool call)
- Symbols have 0 verified until a read_file/write_file tool is used on that file
- Not all files have symbols: only files touched via tool hooks are extracted
- Stale operational files (cron, sessions, state): expected workspace churn
- `DeriveSymbolUsage` creates package-level uses edges — imprecise but structurally sound; extractor-observed edges from actual reads take precedence via upsert

### Web API endpoints (`web/backend/api/swl.go`)

```
GET  /graph               Graph data (modes: map, overview, session)
GET  /graph/neighborhood  Neighborhood of a node
GET  /stats               Aggregate statistics
GET  /health              SWL health check
GET  /sessions            Session list
GET  /overview            Workspace overview snapshot
GET  /model-reliability   Per-model assertion reliability (ADDENDUM 1)
SSE  /stream              Live graph event stream (nodes + edges)
```

---

## CI/CD

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `pr.yml` | Every PR | Lint + govulncheck + tests |
| `build.yml` | Push to main | Multi-platform binary build |
| `release.yml` | Manual dispatch | GoReleaser release (tag + optional draft/prerelease) |
| `nightly.yml` | Daily midnight UTC | Prerelease nightly build |
| `docker-build.yml` | Push to main | Multi-arch Docker push (amd64, arm64, riscv64) to GHCR + Docker Hub |
| `stale.yml` | Scheduled | Stale issue/PR management |

Multi-arch targets: x86_64, ARM, ARM64, RISC-V, LoongArch, MIPS, s390x  
Nightly version format: `v*-nightly.YYYYMMDD.SHA`

---

## Git & PR Conventions

- **Branch from `main`, target `main`**; never push directly to `main`
- **Naming:** `fix/description`, `feat/description`, `docs/description`
- **Commits:** imperative mood, English, [Conventional Commits](https://www.conventionalcommits.org/) format
- **Squash merge strategy:** each PR becomes one commit like `feat: Add X (#123)`
- **PR template required:** description, type, AI disclosure, test environment, evidence
- Do not force-push after review has started; add new commits instead

### Requirements to merge

1. CI passes (lint + test + build)
2. At least one maintainer approval
3. No unresolved review comments
4. PR template complete (AI disclosure section is mandatory)

---

## Configuration

Copy the template to get started:
```bash
cp config/config.example.json ~/.picoclaw/config.json
cp .env.example .env   # fill in API keys — never commit .env
```

Runtime files live in `~/.picoclaw/` (sessions, workspace data) — all gitignored.

---

## Key Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/anthropics/anthropic-sdk-go` | Anthropic Claude API |
| `github.com/openai/openai-go/v3` | OpenAI-compatible APIs |
| `github.com/modelcontextprotocol/go-sdk` | MCP protocol |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/rs/zerolog` | Structured logging |
| `github.com/stretchr/testify` | Test assertions |
| `modernc.org/sqlite` | SQLite, pure Go, no CGO |
| `fyne.io/systray` | System tray (launcher) |
| `github.com/mymmrac/telego` | Telegram |
| `github.com/bwmarrin/discordgo` | Discord (uses fork: yeongaori/discordgo-fork) |
| `go.mau.fi/whatsmeow` | WhatsApp |
| `maunium.net/go/mautrix` | Matrix |
| `github.com/aws/aws-sdk-go-v2` | AWS Bedrock |
| `github.com/gorilla/websocket` | WebSocket |
| `gopkg.in/yaml.v3` | YAML (rules, config) |

Note: `discordgo` is replaced with a fork via `go.mod` replace directive — do not remove.

---

## Reviewer Assignments

| Area | Reviewers |
|------|-----------|
| Provider | @yinwm |
| Channel | @yinwm, @alexhoshina |
| Agent | @lxowalle, @Zhaoyikaiii |
| Tools | @lxowalle |
| Optimization | @lxowalle |
| AI CI | @imguoguo |
