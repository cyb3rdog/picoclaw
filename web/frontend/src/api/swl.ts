import { launcherFetch } from "@/api/http"

export interface SWLNode {
  id: string
  type: string
  name: string
  confidence: number
  factStatus: "unknown" | "verified" | "stale" | "deleted"
  knowledgeDepth: number
  accessCount: number
  metadata?: Record<string, unknown>
}

export interface SWLLink {
  source: string
  target: string
  rel: string
  sessionId?: string
}

export interface SWLGraphMeta {
  nodeCount: number
  linkCount: number
  /** Total non-deleted entities in the DB — used to show "X of Y" scale indicator. */
  totalNodes: number
  /** Total edges in the DB. */
  totalEdges: number
  buildTime: string
  /** Which server-side selection mode produced this graph. */
  mode: "map" | "overview" | "session" | "neighborhood"
}

export interface SWLGraphData {
  nodes: SWLNode[]
  links: SWLLink[]
  meta: SWLGraphMeta
}

export interface SWLStatRow {
  type: string
  total: number
  verified: number
  stale: number
  unknown: number
}

export interface SWLStats {
  rows: SWLStatRow[]
  edgeCount: number
  dbPath: string
}

export interface SWLSession {
  id: string
  startedAt: string
  endedAt?: string
  goal?: string
  summary?: string
}

export interface SWLHealth {
  score: number
  level: "empty" | "poor" | "fair" | "good" | "excellent"
  entityCount: number
  verifiedPct: number
  stalePct: number
  edgeCount: number
  isolatedCount: number
  dbSizeBytes: number
  message: string
}

export interface SWLOverview {
  stats: SWLStats
  health: SWLHealth
  sessions: SWLSession[]
}

/** UI view modes.
 *  "map"          — general graph, top 500 quality-ranked nodes
 *  "overview"     — structural graph, no Symbol/Section, ~150 nodes
 *  "session"      — scoped to recent session activity, ~200 nodes
 *  "neighborhood" — 2-hop subgraph around a specific node (focus mode)
 */
export type SWLViewMode = "map" | "overview" | "session" | "neighborhood"

export const swlApi = {
  async getGraph(mode: SWLViewMode = "map"): Promise<SWLGraphData> {
    const url = mode === "neighborhood"
      ? "/api/swl/graph"   // neighborhood is fetched via getNeighborhood
      : `/api/swl/graph?mode=${mode}`
    const res = await launcherFetch(url)
    if (!res.ok) throw new Error(`SWL graph: ${res.status}`)
    return res.json()
  },

  async getNeighborhood(id: string): Promise<SWLGraphData> {
    const res = await launcherFetch(`/api/swl/graph/neighborhood?id=${encodeURIComponent(id)}`)
    if (!res.ok) throw new Error(`SWL neighborhood: ${res.status}`)
    return res.json()
  },

  async getStats(): Promise<SWLStats> {
    const res = await launcherFetch("/api/swl/stats")
    if (!res.ok) throw new Error(`SWL stats: ${res.status}`)
    return res.json()
  },

  async getSessions(): Promise<SWLSession[]> {
    const res = await launcherFetch("/api/swl/sessions")
    if (!res.ok) throw new Error(`SWL sessions: ${res.status}`)
    return res.json()
  },

  async getHealth(): Promise<SWLHealth> {
    const res = await launcherFetch("/api/swl/health")
    if (!res.ok) throw new Error(`SWL health: ${res.status}`)
    return res.json()
  },

  async getOverview(): Promise<SWLOverview> {
    const res = await launcherFetch("/api/swl/overview")
    if (!res.ok) throw new Error(`SWL overview: ${res.status}`)
    return res.json()
  },

  streamUrl(): string {
    return "/api/swl/stream"
  },
}
