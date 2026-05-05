import { useQuery } from "@tanstack/react-query"

import {
  swlApi,
  type SWLHealth,
  type SWLNode,
  type SWLSession,
  type SWLStats as SWLStatsData,
  type SWLOverview,
} from "@/api/swl"
import { NODE_COLORS } from "./swl-graph"

// Convert a numeric THREE.js colour to a CSS hex string
function toHex(n: number): string {
  return `#${n.toString(16).padStart(6, "0")}`
}

const STATUS_META: Record<string, { icon: string; cls: string; label: string }> = {
  verified: { icon: "✓", cls: "text-emerald-500",     label: "Verified" },
  stale:    { icon: "⚠", cls: "text-amber-400",       label: "Stale"    },
  unknown:  { icon: "?", cls: "text-muted-foreground", label: "Unknown"  },
  deleted:  { icon: "✗", cls: "text-red-500/50",      label: "Deleted"  },
}

interface Props {
  selectedNode?:  SWLNode | null
  hiddenTypes:    Set<string>
  onToggleType:   (type: string) => void
  onClearFilter?: () => void
}

export function SWLStats({ selectedNode, hiddenTypes, onToggleType, onClearFilter }: Props) {
  const { data: overview, isFetching } = useQuery<SWLOverview>({
    queryKey:        ["swl-overview"],
    queryFn:         swlApi.getOverview,
    refetchInterval: 20_000,
    retry:           false,
  })

  const stats    = overview?.stats
  const health   = overview?.health
  const sessions = overview?.sessions

  return (
    <div className="space-y-4 p-3 text-xs">

      {/* ── Node inspector ── */}
      {selectedNode ? (
        <NodeInspector node={selectedNode} />
      ) : (
        <div className="text-muted-foreground py-2 text-center text-[11px] opacity-40">
          Click a node to inspect
        </div>
      )}

      {/* ── Graph health ── */}
      {health && <HealthBadge health={health} isFetching={isFetching} />}

      {/* ── Entity type filter / counts ── */}
      {stats && (
        <section>
          <h2 className="text-muted-foreground mb-1.5 font-semibold uppercase tracking-wide">
            Entity Types
            <span className="ml-1 font-normal normal-case opacity-50">· click to filter</span>
          </h2>
          <div className="space-y-0.5">
            {stats.rows.map((row) => {
              const hidden = hiddenTypes.has(row.type)
              const color  = toHex(NODE_COLORS[row.type] ?? 0x888899)
              return (
                <button
                  key={row.type}
                  onClick={() => onToggleType(row.type)}
                  className={`flex w-full items-center gap-2 rounded px-2 py-1 text-left transition-opacity hover:bg-muted/30 ${hidden ? "opacity-30" : "opacity-100"}`}
                >
                  <span
                    className="inline-block h-2 w-2 shrink-0 rounded-full"
                    style={{ backgroundColor: hidden ? "#444" : color }}
                  />
                  <span className="flex-1 truncate font-mono">{row.type}</span>
                  <span className="text-muted-foreground tabular-nums">{row.total}</span>
                  <span className="text-emerald-600 tabular-nums">{row.verified}</span>
                  <span className="text-amber-500 tabular-nums">{row.stale}</span>
                </button>
              )
            })}
          </div>
          <div className="text-muted-foreground mt-1.5 px-2 text-[10px]">
            {stats.edgeCount} edges total
            {hiddenTypes.size > 0 && (
              <button
                onClick={onClearFilter ?? (() => hiddenTypes.forEach((t) => onToggleType(t)))}
                className="ml-2 underline opacity-60 hover:opacity-100"
              >
                show all
              </button>
            )}
          </div>
        </section>
      )}

      {/* ── Recent sessions ── */}
      {sessions && sessions.length > 0 && (
        <section>
          <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
            Sessions
          </h2>
          <div className="space-y-1">
            {sessions.slice(0, 5).map((s) => (
              <SessionRow key={s.id} session={s} />
            ))}
          </div>
        </section>
      )}

    </div>
  )
}

// ── Node inspector ─────────────────────────────────────────────────────────────

function NodeInspector({ node }: { node: SWLNode }) {
  const color  = toHex(NODE_COLORS[node.type] ?? 0x888899)
  const status = STATUS_META[node.factStatus] ?? {
    icon: "?", cls: "text-muted-foreground", label: node.factStatus,
  }

  return (
    <section
      className="rounded-md border border-border/40 bg-muted/10 p-2.5 space-y-2"
      style={{ borderColor: `${color}44` }}
    >
      {/* Header */}
      <div className="flex items-center gap-1.5">
        <span
          className="inline-block h-2.5 w-2.5 shrink-0 rounded-full"
          style={{ backgroundColor: color }}
        />
        <span className="text-muted-foreground">{node.type}</span>
        <span className={`ml-auto font-mono text-[11px] ${status.cls}`}>
          {status.icon} {status.label}
        </span>
      </div>

      {/* Name */}
      <div className="font-mono text-[11px] text-foreground break-all leading-snug">
        {node.name}
      </div>

      {/* Metrics */}
      <div className="grid grid-cols-2 gap-x-3 gap-y-0.5 text-[11px]">
        <span className="text-muted-foreground">Confidence</span>
        <span className="text-right font-mono">{Math.round(node.confidence * 100)}%</span>
        <span className="text-muted-foreground">Depth</span>
        <span className="text-right font-mono" style={{ color: "#8bc34a" }}>
          {"█".repeat(Math.min(node.knowledgeDepth ?? 0, 5))}
          {"░".repeat(Math.max(0, 5 - (node.knowledgeDepth ?? 0)))}
        </span>
        <span className="text-muted-foreground">Accesses</span>
        <span className="text-right font-mono">{node.accessCount ?? 0}</span>
      </div>

      {/* Metadata */}
      {node.metadata && Object.keys(node.metadata).length > 0 && (
        <div className="border-t border-border/30 pt-1.5 space-y-0.5">
          <div className="text-muted-foreground text-[10px] uppercase tracking-wide mb-1">
            Metadata
          </div>
          {Object.entries(node.metadata).slice(0, 6).map(([k, v]) => (
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

// ── Health badge ───────────────────────────────────────────────────────────────

const HEALTH_META: Record<string, { color: string; icon: string }> = {
  excellent: { color: "text-emerald-400", icon: "●" },
  good:      { color: "text-emerald-600", icon: "●" },
  fair:      { color: "text-amber-400",   icon: "●" },
  poor:      { color: "text-red-500",     icon: "●" },
  empty:     { color: "text-muted-foreground", icon: "○" },
}

function HealthBadge({ health, isFetching }: { health: SWLHealth; isFetching: boolean }) {
  const meta = HEALTH_META[health.level] ?? HEALTH_META.empty
  const pct  = Math.round(health.score * 100)
  const bar  = Math.round(health.score * 8)
  const filled = "█".repeat(bar)
  const empty  = "░".repeat(8 - bar)

  return (
    <section className="rounded-md border border-border/30 bg-muted/10 px-2.5 py-2 space-y-1">
      <div className="flex items-center justify-between">
        <span className="text-muted-foreground font-semibold uppercase tracking-wide">
          Graph Health
          {isFetching && <span className="ml-1.5 animate-pulse opacity-40">●</span>}
        </span>
        <span className={`font-mono text-[11px] ${meta.color}`}>
          {meta.icon} {health.level} {pct}%
        </span>
      </div>
      <div className="font-mono text-[10px] text-muted-foreground">
        <span style={{ color: "#8bc34a" }}>{filled}</span>
        <span className="opacity-30">{empty}</span>
        <span className="ml-2">{health.message}</span>
      </div>
      {health.isolatedCount > 0 && (
        <div className="font-mono text-[10px] text-muted-foreground opacity-50">
          {health.isolatedCount} isolated nodes
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
      {!s.endedAt && (
        <span className="ml-1 text-emerald-500">active</span>
      )}
    </div>
  )
}
