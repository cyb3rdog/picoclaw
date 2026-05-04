import { useCallback, useEffect, useRef, useState } from "react"
import ForceGraph3D from "react-force-graph-3d"
import * as THREE from "three"

import { type SWLGraphData, type SWLLink, type SWLNode, swlApi } from "@/api/swl"

// ── Color palette ─────────────────────────────────────────────────────────────
export const NODE_COLORS: Record<string, number> = {
  File:        0x4a9eff,
  Directory:   0x5a6880,
  Symbol:      0x3dd68c,
  Task:        0xf5a623,
  URL:         0x26c6da,
  Session:     0x9c6fe4,
  Note:        0xff7043,
  Topic:       0xe040fb,
  Dependency:  0x8bc34a,
  Command:     0xffd740,
  Commit:      0xef5350,
  Section:     0x29b6f6,
  Intent:      0xce93d8,
  SubAgent:    0x80cbc4,
  Tool:        0xff6b9d,
}

const LINK_COLORS: Record<string, string> = {
  defines:      "#3dd68c55",
  imports:      "#8bc34a44",
  has_task:     "#f5a62344",
  has_section:  "#29b6f644",
  in_dir:       "#5a688044",
  written_in:   "#4a9eff44",
  edited_in:    "#4a9eff44",
  appended_in:  "#4a9eff33",
  read:         "#8888cc33",
  executed:     "#ffd74033",
  fetched:      "#26c6da33",
  deleted:      "#44444433",
  spawned_by:   "#9c6fe433",
  intended_for: "#ce93d833",
}

const TYPE_ICON: Record<string, string> = {
  File: "📄", Directory: "📁", Symbol: "⚡", Task: "✅",
  URL: "🔗", Session: "🕐", Note: "📝", Topic: "🏷",
  Dependency: "📦", Command: "⌨", Commit: "🔀", Section: "§",
  Intent: "🎯", SubAgent: "🤖", Tool: "🔧",
}

function resolveColor(n: SWLNode): number {
  if (n.factStatus === "deleted") return 0x111111
  if (n.factStatus === "stale")   return 0x2a2d3a
  return NODE_COLORS[n.type] ?? 0x888899
}

function resolveRadius(n: SWLNode): number {
  return 2.5 + Math.cbrt(n.accessCount ?? 0) * 1.8 + (n.knowledgeDepth ?? 0) * 0.4
}

// ── Props ─────────────────────────────────────────────────────────────────────

interface Props {
  data: SWLGraphData
  hiddenTypes?: Set<string>
  /** When set, this node is the focus of a neighborhood subgraph view. */
  focusNodeId?: string
  onNodeClick?: (node: SWLNode | null) => void
}

// ── Component ─────────────────────────────────────────────────────────────────

export function SWLGraph({ data, hiddenTypes, focusNodeId, onNodeClick }: Props) {
  const graphRef     = useRef<any>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const bloomAdded   = useRef(false)
  const isFirstMount = useRef(true)
  const [size, setSize] = useState({ w: 800, h: 600 })

  // Truth sources: allNodesRef holds the actual objects the d3 simulation
  // mutates in-place (it writes x/y/z directly onto these objects). Keeping
  // stable references means positions survive across setGraphState calls —
  // d3-force checks for existing x/y/z on each node object and starts there.
  // NOTE: ngraph does NOT do this (it keeps positions internally), which is
  // why we must stay on the d3 engine.
  const allNodesRef    = useRef<Map<string, any>>(
    new Map((data.nodes ?? []).map((n) => [n.id, n])),
  )
  const allLinksRef    = useRef<SWLLink[]>(data.links ?? [])
  const hiddenTypesRef = useRef<Set<string>>(hiddenTypes ?? new Set())
  const focusNodeRef   = useRef<string | undefined>(focusNodeId)

  // sharedGeometry was used for InstancedMesh experiment - now unused, kept for reference
  // const sharedGeometry = useMemo(() => new THREE.SphereGeometry(1, 8, 6), [])

  // graphState is the React prop fed to ForceGraph3D. Only updated via setGraphState.
  const [graphState, setGraphState] = useState<{ nodes: any[]; links: any[] }>(() => ({
    nodes: data.nodes ?? [],
    links: data.links ?? [],
  }))

  // Keep focusNodeRef in sync so buildNodeObject can read it without stale closure.
  useEffect(() => {
    focusNodeRef.current = focusNodeId
  }, [focusNodeId])

  // ── applyFiltered ────────────────────────────────────────────────────────────
  const applyFiltered = useCallback(() => {
    const hidden = hiddenTypesRef.current
    const all    = Array.from(allNodesRef.current.values())
    const visible      = hidden.size === 0 ? all : all.filter((n) => !hidden.has(n.type))
    const visibleIds   = new Set(visible.map((n: any) => n.id as string))
    const visibleLinks = allLinksRef.current.filter((l) => {
      const src = typeof l.source === "object" ? (l.source as any).id : l.source
      const tgt = typeof l.target === "object" ? (l.target as any).id : l.target
      return visibleIds.has(src) && visibleIds.has(tgt)
    })
    requestAnimationFrame(() => {
      setGraphState({ nodes: visible, links: visibleLinks })
    })
  }, [])

  // ── applySSEUpdate ────────────────────────────────────────────────────────────
  // For existing nodes: mutates in-place only. The nodePositionUpdate callback
  // reads these mutated properties every frame and syncs material color/opacity.
  // We deliberately do NOT call setGraphState for property-only updates:
  //   - setGraphState triggers graphData change → library reheats d3 to alpha=1
  //   - warmupTicks run synchronously → main thread blocked for no visual benefit
  //   - (the custom MeshBasicMaterial is not updated by the library's onUpdateObj)
  // For new nodes: adds to allNodesRef then triggers a filtered rebuild (which
  // does call setGraphState, adding the new node to the simulation).
  //
  // In neighborhood mode (focusNodeRef set): skip new-node expansion to avoid
  // ballooning the focus graph with unrelated SSE arrivals.
  const applySSEUpdate = useCallback(
    (updates: SWLNode[]) => {
      const hidden = hiddenTypesRef.current
      const isFocused = focusNodeRef.current !== undefined
      let hasNew = false

      for (const n of updates) {
        const existing = allNodesRef.current.get(n.id)
        if (existing) {
          Object.assign(existing, n) // keeps simulation's x/y/z; nodePositionUpdate handles visuals
        } else if (!isFocused) {
          allNodesRef.current.set(n.id, { ...n })
          if (!hidden.has(n.type)) hasNew = true
        }
      }

      if (hasNew) {
        applyFiltered()
      }
      // property-only updates: no setGraphState — nodePositionUpdate picks them up next frame
    },
    [applyFiltered],
  )

  // ── React Query data refresh ─────────────────────────────────────────────────
  // When the parent fetches a whole new graph (mode switch or neighborhood load),
  // merge the new nodes into allNodesRef in-place rather than replacing the map.
  // This preserves the d3 simulation's object references — replacing breaks link resolution
  // because d3-force resolves link.source/target to object references during initialization.
  useEffect(() => {
    if (isFirstMount.current) {
      isFirstMount.current = false
      return
    }
    // Merge new nodes into allNodesRef in-place
    for (const n of data.nodes ?? []) {
      const existing = allNodesRef.current.get(n.id)
      if (existing) {
        Object.assign(existing, n)
      } else {
        allNodesRef.current.set(n.id, { ...n })
      }
    }
    // Remove nodes that are no longer in the data
    for (const [id, _node] of allNodesRef.current) {
      if (!data.nodes?.some((n) => n.id === id)) {
        allNodesRef.current.delete(id)
      }
    }
    allLinksRef.current = data.links ?? []
    applyFiltered()
  }, [data, applyFiltered])

  // ── Filter changes ────────────────────────────────────────────────────────────
  useEffect(() => {
    hiddenTypesRef.current = hiddenTypes ?? new Set()
    if ((hiddenTypes?.size ?? 0) > 0 || !isFirstMount.current) {
      applyFiltered()
    }
  }, [hiddenTypes, applyFiltered])

  // ── SSE real-time updates ────────────────────────────────────────────────────
  useEffect(() => {
    const es = new EventSource(swlApi.streamUrl())
    let timer: ReturnType<typeof setTimeout>

    es.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data) as { type: string; nodes: SWLNode[] }
        if (msg.type !== "delta" || !msg.nodes?.length) return
        clearTimeout(timer)
        timer = setTimeout(() => applySSEUpdate(msg.nodes), 400)
      } catch {
        // ignore malformed events
      }
    }

    return () => {
      clearTimeout(timer)
      es.close()
    }
  }, [applySSEUpdate])

  // ── Container resize ─────────────────────────────────────────────────────────
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const apply = () => {
      const w = el.clientWidth
      const h = el.clientHeight
      if (w > 0 && h > 0) setSize({ w, h })
    }
    apply()
    const ro = new ResizeObserver(apply)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // ── Post-mount: bloom, orbit, zoom-to-fit, cursor ────────────────────────────
  useEffect(() => {
    let cancelled = false
    let idleTimer: ReturnType<typeof setTimeout>

    const setup = () => {
      if (cancelled) return
      const fg = graphRef.current
      if (!fg) { requestAnimationFrame(setup); return }

      // Bloom
      if (!bloomAdded.current) {
        const composer = fg.postProcessingComposer?.()
        if (!composer) { requestAnimationFrame(setup); return }
        import("three/addons/postprocessing/UnrealBloomPass.js" as string)
          .catch(() =>
            import("three/examples/jsm/postprocessing/UnrealBloomPass.js" as string),
          )
          .then(({ UnrealBloomPass }: any) => {
            if (cancelled || bloomAdded.current) return
            const { clientWidth: w = 800, clientHeight: h = 600 } =
              containerRef.current ?? {}
            const bp = new UnrealBloomPass(new THREE.Vector2(w, h), 1.2, 0.5, 0.15)
            composer.addPass(bp)
            bloomAdded.current = true
          })
          .catch(() => {})
      }

      // Auto-orbit
      const controls = fg.controls?.()
      const canvas   = fg.renderer?.()?.domElement as HTMLCanvasElement | undefined

      if (controls) {
        controls.autoRotate      = true
        controls.autoRotateSpeed = 0.4

        const pauseOrbit = () => {
          controls.autoRotate = false
          clearTimeout(idleTimer)
          idleTimer = setTimeout(() => {
            if (!cancelled) controls.autoRotate = true
          }, 20_000)
        }
        canvas?.addEventListener("mousedown", pauseOrbit, { passive: true })
        canvas?.addEventListener("touchstart", pauseOrbit, { passive: true })
        canvas?.addEventListener("wheel",      pauseOrbit, { passive: true })
      }

      // Remove pointer cursor
      if (canvas) {
        canvas.addEventListener(
          "mousemove",
          () => { canvas.style.cursor = "default" },
          { passive: true },
        )
      }

      // Zoom-to-fit after physics settles
      setTimeout(() => { if (!cancelled) fg.zoomToFit(800, 60) }, 800)
    }

    requestAnimationFrame(setup)
    return () => {
      cancelled = true
      clearTimeout(idleTimer)
    }
  }, [])

  // ── THREE node objects ────────────────────────────────────────────────────────
  // buildNodeObject creates the mesh once per node (on first appearance).
  // Color and visual state are kept live by updateNodeMaterial (nodePositionUpdate).
  //
  // PERFORMANCE NOTE: For graphs with >5000 nodes, consider switching to InstancedMesh
  // to reduce memory pressure. The current implementation creates individual meshes
  // which works well for up to ~5000 nodes. The shared geometry below helps but
  // InstancedMesh would be a more significant optimization.
  //
  // LOD tiers (based on total visible node count):
  //   < 100 nodes  → 14×10 sphere segs, full quality
  //   100-300 nodes → 8×6 sphere segs
  //   > 300 nodes  → 5×4 sphere segs (minimum readable sphere)
  //
  // Focus node gets a larger radius (+50%) for visual distinction.
  const buildNodeObject = useCallback((rawNode: any) => {
    const n        = rawNode as SWLNode
    const isFocus  = n.id === focusNodeRef.current
    const color    = resolveColor(n)
    const baseR    = resolveRadius(n)
    const r        = isFocus ? baseR * 1.5 : baseR
    const nodeCount = allNodesRef.current.size

    let wSeg: number, hSeg: number
    if (nodeCount < 100)       { wSeg = 14; hSeg = 10 }
    else if (nodeCount < 300)  { wSeg = 8;  hSeg = 6  }
    else                       { wSeg = 5;  hSeg = 4  }

    const isStaleOrDeleted = n.factStatus === "stale" || n.factStatus === "deleted"
    const mesh = new THREE.Mesh(
      new THREE.SphereGeometry(r, wSeg, hSeg),
      new THREE.MeshBasicMaterial({
        color,
        wireframe:   isStaleOrDeleted,
        transparent: isStaleOrDeleted || isFocus,
        opacity:     n.factStatus === "deleted" ? 0.15 : n.factStatus === "stale" ? 0.4 : 1.0,
      }),
    )

    return mesh
  }, [])

  // updateNodeMaterial is called every frame for every node via nodePositionUpdate.
  // It syncs the THREE.js material to the current node data (mutated in-place by
  // applySSEUpdate). This is the only correct way to reflect property changes
  // (factStatus→color, status→wireframe) on custom nodeThreeObject meshes without
  // recreating the mesh (which would lose simulation position) or calling
  // setGraphState (which reheats the simulation for zero visual benefit).
  // Returns false → library still runs the default obj.position update.
  const updateNodeMaterial = useCallback(
    (_obj: any, _coords: { x: number; y: number; z: number }, rawNode: any): boolean => {
      const obj = _obj as THREE.Mesh & { material: THREE.MeshBasicMaterial }
      if (!obj?.material) return false
      const n = rawNode as SWLNode
      const m = obj.material

      const newColor         = resolveColor(n)
      const shouldWireframe  = n.factStatus === "stale" || n.factStatus === "deleted"
      const targetOpacity    = n.factStatus === "deleted" ? 0.15 : n.factStatus === "stale" ? 0.4 : 1.0

      if (m.color.getHex() !== newColor) m.color.setHex(newColor)
      if (m.wireframe !== shouldWireframe) m.wireframe = shouldWireframe
      if (m.transparent !== shouldWireframe) m.transparent = shouldWireframe
      if (shouldWireframe && m.opacity !== targetOpacity) m.opacity = targetOpacity

      return false // let the library write obj.position from the simulation
    },
    [],
  )

  const getLinkColor = useCallback(
    (link: any) => LINK_COLORS[(link as SWLLink).rel] ?? "#2a2d3a55",
    [],
  )

  const getNodeLabel = useCallback((rawNode: any) => {
    const n      = rawNode as SWLNode
    const hexCol = `#${(NODE_COLORS[n.type] ?? 0x888899).toString(16).padStart(6, "0")}`
    const icon   = TYPE_ICON[n.type] ?? "◉"
    const depth  = Math.min(n.knowledgeDepth ?? 0, 5)
    const bar    = "█".repeat(depth) + "░".repeat(5 - depth)
    const sc     =
      n.factStatus === "verified" ? "#3dd68c"
      : n.factStatus === "stale"  ? "#f5a623"
      : n.factStatus === "deleted"? "#ef5350"
      : "#5a6070"
    const isFocus = n.id === focusNodeRef.current
    const focusBadge = isFocus
      ? `<div style="color:#ffffff88;font-size:9px;margin-bottom:4px;letter-spacing:1px">◎ FOCUS NODE</div>`
      : ""

    return `<div style="background:rgba(14,15,20,0.97);padding:9px 12px;border-radius:8px;font-family:monospace;font-size:11px;color:#a0a8bc;line-height:1.65;border:1px solid ${hexCol}44;box-shadow:0 0 16px ${hexCol}28,0 4px 12px rgba(0,0,0,0.8);max-width:300px">
  ${focusBadge}<div style="color:${hexCol};font-weight:700;font-size:12.5px;margin-bottom:5px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${icon}&nbsp;${n.name}</div>
  <div style="display:flex;gap:7px;font-size:10px;margin-bottom:7px;color:#5a6070">
    <span style="color:${hexCol}cc">${n.type}</span><span>·</span>
    <span style="color:${sc}">${n.factStatus}</span>
  </div>
  <div style="display:grid;grid-template-columns:80px 1fr;row-gap:3px;font-size:10px;column-gap:8px">
    <span style="color:#5a6070">confidence</span><span>${Math.round(n.confidence * 100)}%</span>
    <span style="color:#5a6070">depth</span><span style="color:#8bc34a;letter-spacing:2px">${bar}</span>
    <span style="color:#5a6070">accesses</span><span>${n.accessCount ?? 0}</span>
  </div>
  <div style="margin-top:6px;font-size:9px;color:#5a6070">click to focus neighborhood</div>
</div>`
  }, [])

  const handleNodeClick = useCallback(
    (raw: any) => onNodeClick?.(raw as SWLNode),
    [onNodeClick],
  )
  const handleBgClick = useCallback(() => onNodeClick?.(null), [onNodeClick])

  const nodeCount = graphState.nodes.length

  return (
    <div
      ref={containerRef}
      className="bg-background"
      style={{ width: "100%", height: "100%" }}
    >
      <ForceGraph3D
        ref={graphRef}
        width={size.w}
        height={size.h}
        graphData={graphState}
        nodeId="id"
        nodeThreeObject={buildNodeObject}
        nodeThreeObjectExtend={false}
        nodePositionUpdate={updateNodeMaterial}
        nodeLabel={getNodeLabel}
        linkSource="source"
        linkTarget="target"
        linkColor={getLinkColor}
        linkWidth={0.6}
        linkOpacity={0.3}
        linkDirectionalArrowLength={3.5}
        linkDirectionalArrowRelPos={0.95}
        linkDirectionalParticles={nodeCount > 200 ? 0 : 2}
        linkDirectionalParticleWidth={1.5}
        linkDirectionalParticleColor={getLinkColor}
        linkDirectionalParticleSpeed={0.005}
        enableNodeDrag
        backgroundColor="rgba(0,0,0,0)"
        rendererConfig={{ alpha: true, antialias: nodeCount < 200 }}
        showNavInfo={false}
        onNodeClick={handleNodeClick}
        onBackgroundClick={handleBgClick}
        warmupTicks={40}
        cooldownTime={2500}
        d3AlphaDecay={0.03}
        d3VelocityDecay={0.3}
      />
    </div>
  )
}
