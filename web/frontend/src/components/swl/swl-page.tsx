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

// The two navigation modes exposed in the UI.
// NOTE: Session mode has been removed per remediation plan — sessions are audit trail,
// not a semantic organizing principle. Session edges remain in the graph as color differentiation.
const VIEW_MODES: { id: SWLViewMode; label: string; title: string }[] = [
  { id: "overview", label: "overview", title: "Structural graph: files, directories, dependencies (~150 nodes, no Symbol/Section noise)" },
  { id: "map",      label: "map",      title: "General graph: top quality-ranked nodes" },
]

export function SWLPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [selectedNode, setSelectedNode]   = useState<SWLNode | null>(null)
  const [hiddenTypes,  setHiddenTypes]    = useState<Set<string>>(new Set())
  const [showStats,    setShowStats]      = useState(false)
  const [viewMode,     setViewMode]       = useState<SWLViewMode>("overview")
  // focusNodeId drives the "neighborhood" subgraph fetch — null = not in focus mode.
  const [focusNodeId,  setFocusNodeId]    = useState<string | null>(null)
  // Breadcrumb: the mode the user was in before entering focus mode.
  const [preNavMode,   setPreNavMode]     = useState<SWLViewMode>("overview")

  // Main graph query (session / overview / map).
  const {
    data: graphData,
    isLoading,
    error,
    refetch,
  } = useQuery<SWLGraphData>({
    queryKey: ["swl-graph", viewMode],
    queryFn:  () => swlApi.getGraph(viewMode),
    enabled:  focusNodeId === null,
    retry:    false,
    // No timed auto-refetch: SSE stream delivers all incremental updates.
    // Users can trigger a manual reload via the Reload button.
  })

  // Neighborhood query — only active when a node is focused.
  const {
    data: neighborData,
    isLoading: neighborLoading,
  } = useQuery<SWLGraphData>({
    queryKey: ["swl-neighborhood", focusNodeId],
    queryFn:  () => swlApi.getNeighborhood(focusNodeId!),
    enabled:  focusNodeId !== null,
    retry:    false,
  })

  const activeData = focusNodeId !== null ? neighborData : graphData
  const activeLoading = focusNodeId !== null ? neighborLoading : isLoading

  const handleToggleType = useCallback((type: string) => {
    setHiddenTypes((prev) => {
      const next = new Set(prev)
      if (next.has(type)) next.delete(type)
      else next.add(type)
      return next
    })
  }, [])

  const handleSelectMode = useCallback((mode: SWLViewMode) => {
    if (focusNodeId !== null) {
      // Exiting focus mode — clear the focused node.
      setFocusNodeId(null)
      setSelectedNode(null)
    }
    setViewMode(mode)
    setHiddenTypes(new Set())
    // Warm up adjacent mode caches.
    queryClient.prefetchQuery({
      queryKey: ["swl-graph", mode],
      queryFn:  () => swlApi.getGraph(mode),
    })
  }, [focusNodeId, queryClient])

  const handleNodeClick = useCallback((node: SWLNode | null) => {
    setSelectedNode(node)
    if (node) {
      // Enter focus mode for the clicked node.
      setPreNavMode(focusNodeId !== null ? preNavMode : viewMode)
      setFocusNodeId(node.id)
    }
  }, [focusNodeId, preNavMode, viewMode])

  const handleExitFocus = useCallback(() => {
    setFocusNodeId(null)
    setSelectedNode(null)
    setViewMode(preNavMode)
  }, [preNavMode])

  if (activeLoading && !activeData) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-muted-foreground text-sm">Loading knowledge graph…</p>
      </div>
    )
  }

  if (error && !activeData) {
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

  const meta = activeData?.meta
  const scaleLabel = meta && meta.totalNodes > 0
    ? `${meta.nodeCount} of ${meta.totalNodes.toLocaleString()} nodes · ${meta.linkCount} edges`
    : meta
    ? `${meta.nodeCount} nodes · ${meta.linkCount} edges`
    : null

  const metaExtra = scaleLabel && (
    <span className="text-muted-foreground text-sm font-normal">
      {scaleLabel}
      {selectedNode && !focusNodeId && (
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
        {/* Focus mode breadcrumb */}
        {focusNodeId && selectedNode && (
          <div className="flex items-center gap-1.5 text-xs font-mono">
            <button
              onClick={handleExitFocus}
              className="text-muted-foreground hover:text-foreground transition-colors"
              title={`Back to ${preNavMode} view`}
            >
              ← {preNavMode}
            </button>
            <span className="text-muted-foreground opacity-40">/</span>
            <span className="text-foreground truncate max-w-[120px]" title={selectedNode.name}>
              {selectedNode.name}
            </span>
          </div>
        )}

        {/* View mode selector — hidden while in focus mode */}
        {!focusNodeId && (
          <div className="flex items-center gap-1">
            {VIEW_MODES.map(({ id, label, title }) => (
              <button
                key={id}
                onClick={() => handleSelectMode(id)}
                className={cn(
                  "rounded border px-2.5 py-1 text-xs font-mono transition-colors",
                  viewMode === id
                    ? "border-accent bg-accent text-foreground"
                    : "border-border text-muted-foreground hover:text-foreground",
                )}
                title={title}
              >
                {label}
              </button>
            ))}
          </div>
        )}

        {/* Stats toggle */}
        <Button
          variant="ghost"
          size="icon"
          aria-label="Toggle stats panel"
          className={cn("h-9 w-9", showStats && "bg-accent text-foreground")}
          onClick={() => setShowStats((v) => !v)}
        >
          <IconLayoutSidebarRight className="size-5" />
        </Button>
      </PageHeader>

      {/* ── Main area ──────────────────────────────────────────────────────── */}
      <div className="relative flex flex-1 overflow-hidden">

        {/* Graph */}
        <div className="flex-1 overflow-hidden">
          {activeData && (
            <SWLGraph
              data={activeData}
              hiddenTypes={hiddenTypes}
              focusNodeId={focusNodeId ?? undefined}
              onNodeClick={handleNodeClick}
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
