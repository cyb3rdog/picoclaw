import { useQuery } from "@tanstack/react-query"

import { swlApi, type SWLStats as SWLStatsData, type SWLSession } from "@/api/swl"

export function SWLStats() {
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
      {stats && (
        <section>
          <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
            Entities
          </h2>
          <table className="w-full">
            <thead>
              <tr className="text-muted-foreground">
                <th className="text-left font-normal">Type</th>
                <th className="text-right font-normal">Total</th>
                <th className="text-right font-normal">✓</th>
                <th className="text-right font-normal">⚠</th>
              </tr>
            </thead>
            <tbody>
              {stats.rows.map((row) => (
                <tr key={row.type} className="border-t border-muted/20">
                  <td className="py-0.5 font-mono">{row.type}</td>
                  <td className="text-right">{row.total}</td>
                  <td className="text-right text-green-600">{row.verified}</td>
                  <td className="text-right text-yellow-500">{row.stale}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="text-muted-foreground mt-1">
            {stats.edgeCount} edges total
          </div>
        </section>
      )}

      {sessions && sessions.length > 0 && (
        <section>
          <h2 className="text-muted-foreground mb-1 font-semibold uppercase tracking-wide">
            Recent Sessions
          </h2>
          <div className="space-y-1">
            {sessions.slice(0, 5).map((s) => (
              <div key={s.id} className="truncate">
                <span className="font-mono text-muted-foreground">
                  {s.id.slice(0, 8)}
                </span>
                {s.goal && (
                  <span className="ml-1 truncate">{s.goal.slice(0, 40)}</span>
                )}
                {!s.endedAt && (
                  <span className="ml-1 text-green-500">active</span>
                )}
              </div>
            ))}
          </div>
        </section>
      )}

      <SWLLegend />
    </div>
  )
}

const NODE_TYPE_COLORS: [string, string][] = [
  ["File", "#4488ff"],
  ["Directory", "#888888"],
  ["Symbol", "#44ff88"],
  ["Task", "#ffcc44"],
  ["URL", "#44ffff"],
  ["Session", "#cc44ff"],
  ["Note", "#ff8844"],
  ["Topic", "#ff4488"],
  ["Dependency", "#88ff44"],
  ["Command", "#ffff44"],
  ["Commit", "#ff4444"],
  ["Section", "#44ccff"],
  ["Intent", "#ff88cc"],
  ["SubAgent", "#88ccff"],
]

function SWLLegend() {
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
