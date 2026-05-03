import { IconLayoutSidebarRight } from "@tabler/icons-react"
import { useCallback, useState } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { Link } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"

import { swlApi, type SWLGraphData, type SWLNode, type SWLViewMode } from "@/api/swl"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"
import { SWLGraph } from "./swl-graph"
import { SWLStats } from "./swl-stats"

export function SWLPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selectedNode, setSelectedNode] = useState<SWLNode | null>(null)
  const [hiddenTypes,  setHiddenTypes]  = useState<Set<string>>(new Set())
  const [showStats,    setShowStats]    = useState(false)
  const [viewMode,     setViewMode]     = useState<SWLViewMode>("full")

  const {
    data: graphData,
    isLoading,
    error,
    refetch,
  } = useQuery<SWLGraphData>({
    queryKey: ["swl-graph", viewMode],
    queryFn:  () => swlApi.getGraph(viewMode),
    retry:    false,
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

  const handleToggleView = useCallback(() => {
    const next: SWLViewMode = viewMode === "full" ? "overview" : "full"
    setViewMode(next)
    setSelectedNode(null)
    setHiddenTypes(new Set())
    // Prefetch the other view in the background for instant switching
    queryClient.prefetchQuery({
      queryKey: ["swl-graph", viewMode],
      queryFn:  () => swlApi.getGraph(viewMode),
    })
  }, [viewMode, queryClient])

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

  const metaExtra = graphData && (
    <span className="text-muted-foreground text-sm font-normal">
      {graphData.meta.nodeCount} nodes · {graphData.meta.linkCount} edges
      {selectedNode && (
        <span className="ml-2 opacity-60">· {selectedNode.name}</span>
      )}
    </span>
  )

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.swl")}
        titleExtra={metaExtra}
      >
        {/* View mode toggle */}
        <button
          onClick={handleToggleView}
          className={cn(
            "rounded border px-2.5 py-1 text-xs font-mono transition-colors",
            viewMode === "overview"
              ? "border-accent bg-accent text-foreground"
              : "border-border text-muted-foreground hover:text-foreground",
          )}
          title={viewMode === "full" ? "Switch to overview (structural nodes only)" : "Switch to full view (all 500 nodes)"}
        >
          {viewMode === "overview" ? "overview" : "full"}
        </button>

        {/* Stats toggle */}
        <Button
          variant="ghost"
          size="icon"
          aria-label="Toggle stats panel"
          className={cn(
            "h-9 w-9",
            showStats && "bg-accent text-foreground",
          )}
          onClick={() => setShowStats((v) => !v)}
        >
          <IconLayoutSidebarRight className="size-5" />
        </Button>
      </PageHeader>

      {/* ── Main area ──────────────────────────────────────────────────────── */}
      <div className="relative flex flex-1 overflow-hidden">

        {/* Graph */}
        <div className="flex-1 overflow-hidden">
          {graphData && (
            <SWLGraph
              data={graphData}
              hiddenTypes={hiddenTypes}
              onNodeClick={setSelectedNode}
            />
          )}
        </div>

        {/* ── Desktop side panel (sm+) ── */}
        <div
          className={cn(
            "hidden sm:flex sm:flex-col sm:shrink-0 sm:overflow-y-auto sm:border-l sm:transition-all sm:duration-200",
            showStats ? "sm:w-72" : "sm:w-0 sm:overflow-hidden sm:border-l-0",
          )}
        >
          {showStats && (
            <SWLStats
              selectedNode={selectedNode}
              hiddenTypes={hiddenTypes}
              onToggleType={handleToggleType}
              onClearFilter={() => setHiddenTypes(new Set())}
            />
          )}
        </div>

        {/* ── Mobile bottom sheet ── */}
        {showStats && (
          <div className="absolute inset-x-0 bottom-0 z-20 flex max-h-[60vh] flex-col overflow-hidden rounded-t-xl border-t bg-background shadow-2xl sm:hidden">
            <div className="flex shrink-0 justify-center py-2">
              <div className="bg-muted-foreground/30 h-1 w-10 rounded-full" />
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto">
              <SWLStats
                selectedNode={selectedNode}
                hiddenTypes={hiddenTypes}
                onToggleType={handleToggleType}
                onClearFilter={() => setHiddenTypes(new Set())}
              />
            </div>
          </div>
        )}

        {/* Backdrop to close mobile sheet on tap */}
        {showStats && (
          <div
            className="absolute inset-0 z-10 sm:hidden"
            onClick={() => setShowStats(false)}
          />
        )}
      </div>
    </div>
  )
}
