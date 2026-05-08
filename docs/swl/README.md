# SWL Documentation

> The authoritative source for SWL (Semantic Workspace Layer) design and implementation.

## Documents

| Document | Purpose |
|----------|---------|
| **[SWL-DESIGN.md](./SWL-DESIGN.md)** | Full system design — purpose, vision, mission, principles, conceptual model, extraction tiers, query model, feedback loop, system architecture, configuration, known behaviors |
| **[SWL-REFACTOR.md](./SWL-REFACTOR.md)** | Live refactor tracker — root causes, design principles, phase sequencing with verification criteria, current KG state, Phase A/B/C status |
| [swl-refactor-plan.md](./swl-refactor-plan.md) | **Legacy** — original plan, superseded by SWL-REFACTOR.md |
| [swl-refactor-requirements.md](./swl-refactor-requirements.md) | **Legacy** — original requirements, consolidated into SWL-DESIGN.md |

## Quick Reference

```bash
# Query the knowledge graph
query_swl {"resume":true}                   # session resume digest
query_swl {"stats":true}                    # entity/edge counts by type
query_swl {"gaps":true}                     # unknown/low-confidence entities
query_swl {"drift":true}                    # stale/outdated entities
query_swl {"question":"what is this workspace for?"}
query_swl {"question":"where is the file that does X?"}
query_swl {"question":"what does pkg/foo/bar.go do?"}
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
GET /api/swl/stream    # SSE delta stream
GET /api/swl/topology  # gzip paginated full graph
```

## Refactor Status

| Phase | Status | Description |
|-------|--------|-------------|
| A | ✅ Done | BuildSnapshot, lazy extraction, events table, access_count fix, gap recording |
| B | ⏳ Pending | Externalize to swl.rules.yaml + swl.query.yaml |
| C | ⏳ Pending | Autonomous feedback loop with cross-agent convergence |

See [SWL-REFACTOR.md](./SWL-REFACTOR.md) for detailed phase contents and verification criteria.
