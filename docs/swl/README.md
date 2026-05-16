# SWL Documentation

> The authoritative source for SWL (Semantic Workspace Layer) design and implementation.

## Documents

| Document | Purpose |
|----------|---------|
| **[SWL-DESIGN.md](./SWL-DESIGN.md)** | Full system design — purpose, vision, principles, extraction tiers, query model, feedback loop, architecture, configuration, known gaps |
| **[ROADMAP.md](./ROADMAP.md)** | Intentionally deferred features with rationale |
| **[swl-refactor-requirements.md](./swl-refactor-requirements.md)** | Canonical verbatim requirements — source of truth for the original intent |

## Quick Reference

```bash
# Query the knowledge graph
query_swl {"resume":true}                   # session resume digest
query_swl {"help":true}                     # query syntax reference
query_swl {"stats":true}                    # entity/edge counts by type
query_swl {"gaps":true}                     # unknown/low-confidence entities + rule suggestions
query_swl {"drift":true}                    # stale/outdated entities
query_swl {"question":"what is this workspace for?"}
query_swl {"question":"where is the file that does X?"}
query_swl {"question":"what does pkg/foo/bar.go do?"}
query_swl {"question":"model reliability"}  # per-model assertion stats (ADDENDUM 1)
query_swl {"assert":"note text","subject":"topic","confidence":0.9}
query_swl {"scan":true,"root":"."}          # incremental workspace index
query_swl {"sql":"SELECT ..."}              # raw read-only SQL

# SWL web UI
# Navigate to: /swl (requires SWL enabled in config)

# Web API
GET /api/swl/graph?mode=map|overview|session
GET /api/swl/graph/neighborhood?id=<nodeId>
GET /api/swl/stats
GET /api/swl/health
GET /api/swl/sessions
GET /api/swl/overview
GET /api/swl/model-reliability             # per-model assertion reliability (ADDENDUM 1)
GET /api/swl/stream                        # SSE delta stream (nodes + edges)
```

## Phase Status

| Phase | Status | Description |
|-------|--------|-------------|
| A | ✅ Done | BuildSnapshot, lazy extraction, events table, access_count fix, gap recording |
| A.2 | ✅ Done | Path pattern → semantic label derivation (Tier 1 inference, no LLM) |
| A.3 | ✅ Done | labelSearch handler, 30+ Tier 1 intent patterns, label-weighted scoring |
| B | ✅ Done | YAML rules framework, externalized extraction + query logic, workspace overrides |
| C | ✅ Done | Autonomous feedback loop, gap → candidate rule generation, inline suggestions |
| D | ✅ Done | SSE edge delivery, assertion-in-metadata, Intent/SubAgent wiring |
| N+1 | ✅ Done | Graph provenance (context_of), evidence-based decay, DeriveSymbolUsage, per-model reliability (ADDENDUM 1) |

See [SWL-DESIGN.md](./SWL-DESIGN.md) for full architecture details.
See [ROADMAP.md](./ROADMAP.md) for intentionally deferred items.
