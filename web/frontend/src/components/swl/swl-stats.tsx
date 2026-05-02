import { useQuery } from "@tanstack/react-query"

import {
  swlApi,
  type SWLNode,
  type SWLSession,
  type SWLStats as SWLStatsData,
} from "@/api/swl"

const NODE_TYPE_COLORS: [string, string][] = [
  ["File", "#4499ff"],
  ["Directory", "#778899"],
  ["Symbol", "#44ff99"],
  ["Task", "#ffcc33"],
  ["URL", "#33ffee"],
  ["Session", "#cc44ff"],
  ["Note", "#ff8833"],
  ["Topic", "#ff3388"],
  ["Dependency", "#88ff33"],
  ["Command", "#ffee33"],
  ["Commit", "#ff4444"],
  ["Section", "#33ccff"],
  ["Intent", "#ff88cc"],
  ["SubAgent", "#88ccff"],
]

const COLOR_BY_TYPE: Record<string, string> = Object.fromEntries(NODE_TYPE_COLORS)

const STATUS_META: Record<string, { icon: string; cls: string; label: string }> = {
  verified: { icon: "✓", cls: "text-green-500", label: "Verified" },
  stale: { icon: "⚠", cls: "text-yellow-500", label: "Stale" },
  unknown: { icon: "?", cls: "text-muted-foreground", label: "Unknown" },
  deleted: { icon: "✗", cls: "text-red-500/50", label: "Deleted" },
}

interface Props {
  selectedNode?: SWLNode | null
}

export function SWLStats({ selectedNode }: Props) {
  const { data: stats } = useQuery<SWLStatsData>({
    queryKey: ["swl-stats"],
    queryFn: swlApi.getStats,
    refetchInterval: 15_000,
    retry: false,
  })

  const { data: sessions } = useQuery<SWLSession[]>({
    queryKey: ["swl-sessions"],
    queryFn: swlApi.getSessions,
    refetchInterval: 30_000,
    retry: false,
  })

  return (
    <div className="space-y-4 p-3 text-xs">
      {/* ── Node inspector ── */}
      {selectedNode ? (
        <NodeInspector node={selectedNode} />
      ) : (
        <div className="text-muted-foreground py-2 text-center text-[11px] opacity-50">
          Click a node to inspect
        </div>
      )}

      {/* ── Entity counts ── */}
      {stats && (
        <section>
          <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
            Entities
          </h2>
          <table className="w-full">
            <thead>
              <tr className="text-muted-foreground">
                <th className="text-left font-normal">Type</th>
                <th className="text-right font-normal">All</th>
                <th className="text-right font-normal">✓</th>
                <th className="text-right font-normal">⚠</th>
              </tr>
            </thead>
            <tbody>
              {stats.rows.map((row) => (
                <tr key={row.type} className="border-t border-muted/20">
                  <td className="py-0.5">
                    <div className="flex items-center gap-1">
                      <span
                        className="inline-block h-1.5 w-1.5 shrink-0 rounded-full"
                        style={{
                          backgroundColor: COLOR_BY_TYPE[row.type] ?? "#666",
                        }}
                      />
                      <span className="font-mono">{row.type}</span>
                    </div>
                  </td>
                  <td className="text-right">{row.total}</td>
                  <td className="text-right text-green-600">{row.verified}</td>
                  <td className="text-right text-yellow-500">{row.stale}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="text-muted-foreground mt-1">{stats.edgeCount} edges total</div>
        </section>
      )}

      {/* ── Recent sessions ── */}
      {sessions && sessions.length > 0 && (
        <section>
          <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
            Recent Sessions
          </h2>
          <div className="space-y-1">
            {sessions.slice(0, 5).map((s) => (
              <SessionRow key={s.id} session={s} />
            ))}
          </div>
        </section>
      )}

      <NodeLegend />
    </div>
  )
}

// ── Node inspector ─────────────────────────────────────────────────────────────

function NodeInspector({ node }: { node: SWLNode }) {
  const color = COLOR_BY_TYPE[node.type] ?? "#888"
  const status = STATUS_META[node.factStatus] ?? {
    icon: "?",
    cls: "text-muted-foreground",
    label: node.factStatus,
  }

  return (
    <section className="rounded-md border border-border/40 bg-muted/10 p-2.5 space-y-2">
      {/* Header: type badge + status */}
      <div className="flex items-center gap-1.5">
        <span
          className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
        <span className="text-muted-foreground">{node.type}</span>
        <span
          className={`ml-auto font-mono text-[11px] ${status.cls}`}
          title={status.label}
        >
          {status.icon} {status.label}
        </span>
      </div>

      {/* Name */}
      <div className="font-mono text-[11px] text-foreground break-all leading-snug">
        {node.name}
      </div>

      {/* Key metrics */}
      <div className="grid grid-cols-2 gap-x-3 gap-y-0.5 text-[11px]">
        <span className="text-muted-foreground">Confidence</span>
        <span className="text-right font-mono">
          {Math.round(node.confidence * 100)}%
        </span>
        <span className="text-muted-foreground">Knowledge depth</span>
        <span className="text-right font-mono">{node.knowledgeDepth}</span>
        <span className="text-muted-foreground">Access count</span>
        <span className="text-right font-mono">{node.accessCount}</span>
      </div>

      {/* Metadata */}
      {node.metadata && Object.keys(node.metadata).length > 0 && (
        <div className="border-t border-border/30 pt-1.5 space-y-0.5">
          <div className="text-muted-foreground text-[10px] uppercase tracking-wide mb-1">
            Metadata
          </div>
          {Object.entries(node.metadata)
            .slice(0, 6)
            .map(([k, v]) => (
              <div key={k} className="flex justify-between gap-2 font-mono text-[10px]">
                <span className="text-muted-foreground truncate">{k}</span>
                <span className="text-foreground truncate max-w-[110px]">
                  {typeof v === "string" ? v : JSON.stringify(v)}
                </span>
              </div>
            ))}
        </div>
      )}
    </section>
  )
}

// ── Session row ────────────────────────────────────────────────────────────────

function SessionRow({ session: s }: { session: SWLSession }) {
  return (
    <div className="truncate">
      <span className="font-mono text-muted-foreground">{s.id.slice(0, 8)}</span>
      {s.goal && <span className="ml-1 truncate">{s.goal.slice(0, 40)}</span>}
      {!s.endedAt && <span className="ml-1 text-green-500">active</span>}
    </div>
  )
}

// ── Node type legend ───────────────────────────────────────────────────────────

function NodeLegend() {
  return (
    <section>
      <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
        Node Types
      </h2>
      <div className="grid grid-cols-2 gap-x-2 gap-y-0.5">
        {NODE_TYPE_COLORS.map(([type, color]) => (
          <div key={type} className="flex items-center gap-1">
            <span
              className="inline-block h-2 w-2 shrink-0 rounded-full"
              style={{ backgroundColor: color }}
            />
            <span className="truncate font-mono">{type}</span>
          </div>
        ))}
      </div>
    </section>
  )
}
