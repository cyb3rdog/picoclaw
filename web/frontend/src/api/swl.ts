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

export interface SWLGraphData {
  nodes: SWLNode[]
  links: SWLLink[]
  meta: {
    nodeCount: number
    linkCount: number
    buildTime: string
  }
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

export const swlApi = {
  async getGraph(): Promise<SWLGraphData> {
    const res = await launcherFetch("/api/swl/graph")
    if (!res.ok) throw new Error(`SWL graph: ${res.status}`)
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

  streamUrl(): string {
    return "/api/swl/stream"
  },
}
