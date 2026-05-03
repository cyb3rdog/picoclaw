import { useCallback, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import { swlApi, type SWLGraphData, type SWLNode } from "@/api/swl"
import { SWLGraph } from "./swl-graph"
import { SWLStats } from "./swl-stats"

export function SWLPage() {
  const { t } = useTranslation()
  const [selectedNode, setSelectedNode] = useState<SWLNode | null>(null)
  const [hiddenTypes,  setHiddenTypes]  = useState<Set<string>>(new Set())

  const {
    data: graphData,
    isLoading,
    error,
    refetch,
  } = useQuery<SWLGraphData>({
    queryKey: ["swl-graph"],
    queryFn:  swlApi.getGraph,
    retry:    false,
    // Periodic refresh keeps links up-to-date (nodes arrive sooner via SSE).
    refetchInterval: 30_000,
  })

  const handleToggleType = useCallback((type: string) => {
    setHiddenTypes((prev) => {
      const next = new Set(prev)
      if (next.has(type)) next.delete(type)
      else next.add(type)
      return next
    })
  }, [])

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-muted-foreground text-sm">Loading knowledge graph…</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3">
        <p className="text-muted-foreground max-w-xs text-center text-sm">
          Knowledge graph unavailable. Enable SWL in config to start building it.
        </p>
        <Link to="/config" className="text-xs underline opacity-60 hover:opacity-100">
          Open Config →
        </Link>
        <button
          className="mt-1 rounded border px-3 py-1 text-xs opacity-70 hover:opacity-100"
          onClick={() => refetch()}
        >
          Retry
        </button>
      </div>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <div className="border-b px-4 py-2">
        <h1 className="text-sm font-semibold">{t("navigation.swl")}</h1>
        {graphData && (
          <p className="text-muted-foreground text-xs">
            {graphData.meta.nodeCount} nodes · {graphData.meta.linkCount} edges
            {selectedNode && (
              <span className="ml-2 opacity-60">· {selectedNode.name}</span>
            )}
          </p>
        )}
      </div>

      <div className="flex flex-1 overflow-hidden">
        <div className="flex-1 overflow-hidden">
          {graphData && (
            <SWLGraph
              data={graphData}
              hiddenTypes={hiddenTypes}
              onNodeClick={setSelectedNode}
            />
          )}
        </div>
        <div className="w-72 shrink-0 overflow-y-auto border-l">
          <SWLStats
            selectedNode={selectedNode}
            hiddenTypes={hiddenTypes}
            onToggleType={handleToggleType}
          />
        </div>
      </div>
    </div>
  )
}
