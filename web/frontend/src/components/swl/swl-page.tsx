import { useQuery } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { swlApi, type SWLGraphData } from "@/api/swl"
import { SWLGraph } from "./swl-graph"
import { SWLStats } from "./swl-stats"

export function SWLPage() {
  const { t } = useTranslation()

  const {
    data: graphData,
    isLoading,
    error,
    refetch,
  } = useQuery<SWLGraphData>({
    queryKey: ["swl-graph"],
    queryFn: swlApi.getGraph,
    refetchInterval: 10_000,
    retry: false,
  })

  if (isLoading) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-muted-foreground text-sm">Loading knowledge graph…</p>
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-4">
        <p className="text-muted-foreground text-sm">
          Knowledge graph unavailable. Enable SWL in config.json to get started.
        </p>
        <pre className="bg-muted max-w-lg rounded p-3 text-xs">
          {`"tools": { "swl": { "enabled": true } }`}
        </pre>
        <button
          className="rounded border px-3 py-1 text-xs"
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
          </p>
        )}
      </div>
      <div className="flex flex-1 overflow-hidden">
        <div className="flex-1 overflow-hidden">
          {graphData && <SWLGraph data={graphData} />}
        </div>
        <div className="w-72 shrink-0 overflow-y-auto border-l">
          <SWLStats />
        </div>
      </div>
    </div>
  )
}
