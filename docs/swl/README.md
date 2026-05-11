# SWL Documentation

> The authoritative source for SWL (Semantic Workspace Layer) design and implementation.

## Documents

| Document | Purpose |
|----------|---------|
| **[SWL-DESIGN.md](./SWL-DESIGN.md)** | Full system design — purpose, vision, principles, extraction tiers, query model, feedback loop, architecture, configuration, known gaps |
| **[SWL-PHASE-B-SPEC.md](./SWL-PHASE-B-SPEC.md)** | Phase B design spec — YAML rules framework, separation of signal types vs. derivation methods. Keep until B1/B3 gaps are closed. |
| **[SWL-PHASE-B-AUDIT.md](./SWL-PHASE-B-AUDIT.md)** | Root-cause audit of Phase B wiring gaps (B1: label rules, B3: extraction limits). Keep until B1/B3 are fixed and verified. |
| **[swl-refactor-requirements.md](./swl-refactor-requirements.md)** | Canonical verbatim requirements — source of truth for R1–R5. |

## Quick Reference

```bash
# Query the knowledge graph
query_swl {"resume":true}                   # session resume digest
query_swl {"help":true}                     # query syntax reference
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

## Phase Status

| Phase | Status | Description |
|-------|--------|-------------|
| A | ✅ Done | BuildSnapshot, lazy extraction, events table, access_count fix, gap recording |
| A.2 | ✅ Done | Path pattern → semantic label derivation (Tier 1 inference, no LLM) |
| A.3 | ✅ Done | labelSearch handler, 30+ Tier 1 intent patterns, label-weighted scoring |
| B | ⚠️ Partial | Infrastructure complete; **B1** (label rules) and **B3** (extraction limits) not wired to YAML |
| C | ✅ Done | Autonomous feedback loop, gap → candidate rule generation |

See [SWL-DESIGN.md § Known Gaps](./SWL-DESIGN.md) for details on open B1/B3 items.
