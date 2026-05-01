import * as React from "react"
import { useEffect, useRef, useCallback } from "react"
import ForceGraph3D from "react-force-graph-3d"

import { type SWLGraphData, type SWLNode, type SWLLink, swlApi } from "@/api/swl"

// Node color by entity type
const NODE_COLORS: Record<string, string> = {
  File: "#4488ff",
  Directory: "#888888",
  Symbol: "#44ff88",
  Task: "#ffcc44",
  URL: "#44ffff",
  Session: "#cc44ff",
  Note: "#ff8844",
  Topic: "#ff4488",
  Dependency: "#88ff44",
  Command: "#ffff44",
  Commit: "#ff4444",
  Section: "#44ccff",
  Intent: "#ff88cc",
  SubAgent: "#88ccff",
}

const LINK_COLORS: Record<string, string> = {
  defines: "#44ff88",
  imports: "#88ff44",
  has_task: "#ffcc44",
  has_section: "#44ccff",
  in_dir: "#666666",
  written_in: "#4488ff",
  edited_in: "#4488ff",
  read: "#aaaaff",
  executed: "#ffff44",
  fetched: "#44ffff",
  spawned_by: "#cc44ff",
  intended_for: "#ff88cc",
}

interface Props {
  data: SWLGraphData
}

export function SWLGraph({ data }: Props) {
  const graphRef = useRef<any>(null)
  const [graphData, setGraphData] = React.useState(data)

  // SSE real-time updates
  useEffect(() => {
    const es = new EventSource(swlApi.streamUrl())
    let debounceTimer: ReturnType<typeof setTimeout>

    es.onmessage = (e) => {
      try {
        const update = JSON.parse(e.data) as { type: string; nodes: SWLNode[] }
        if (update.type !== "delta" || !update.nodes?.length) return

        clearTimeout(debounceTimer)
        debounceTimer = setTimeout(() => {
          setGraphData((prev) => {
            const nodeMap = new Map(prev.nodes.map((n) => [n.id, n]))
            for (const n of update.nodes) {
              nodeMap.set(n.id, n)
            }
            return {
              ...prev,
              nodes: Array.from(nodeMap.values()),
              meta: {
                ...prev.meta,
                nodeCount: nodeMap.size,
              },
            }
          })
        }, 500)
      } catch {
        // ignore malformed events
      }
    }

    return () => {
      clearTimeout(debounceTimer)
      es.close()
    }
  }, [])

  const nodeColor = useCallback(
    (node: any) => {
      const n = node as SWLNode
      if (n.factStatus === "stale") return "#555555"
      if (n.factStatus === "deleted") return "#333333"
      return NODE_COLORS[n.type] ?? "#ffffff"
    },
    [],
  )

  const nodeVal = useCallback((node: any) => {
    const n = node as SWLNode
    return Math.log1p(n.accessCount ?? 0) + 1 + n.knowledgeDepth * 0.5
  }, [])

  const nodeOpacity = useCallback((node: any) => {
    const n = node as SWLNode
    return Math.max(0.3, n.confidence ?? 1.0)
  }, [])

  const linkColor = useCallback((link: any) => {
    return LINK_COLORS[(link as SWLLink).rel] ?? "#444444"
  }, [])

  return (
    <ForceGraph3D
      ref={graphRef}
      graphData={graphData as any}
      nodeId="id"
      nodeColor={nodeColor}
      nodeVal={nodeVal}
      nodeOpacity={nodeOpacity}
      linkSource="source"
      linkTarget="target"
      linkColor={linkColor}
      linkWidth={0.8}
      linkDirectionalArrowLength={4}
      linkDirectionalArrowRelPos={1}
      enableNodeDrag
      backgroundColor="#0a0a0f"
      showNavInfo={false}
    />
  )
}
