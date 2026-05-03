import { useCallback, useEffect, useRef, useState } from "react"
import ForceGraph3D from "react-force-graph-3d"
import * as THREE from "three"

import { type SWLGraphData, type SWLLink, type SWLNode, swlApi } from "@/api/swl"

// ── Color palette ─────────────────────────────────────────────────────────────
// Numbers for THREE.js materials; semantically chosen per entity type.
export const NODE_COLORS: Record<string, number> = {
  File:        0x4a9eff, // blue       — source files / data
  Directory:   0x5a6880, // steel      — folders
  Symbol:      0x3dd68c, // emerald    — functions / exports
  Task:        0xf5a623, // amber      — TODOs / work items
  URL:         0x26c6da, // cyan       — web resources
  Session:     0x9c6fe4, // violet     — agent sessions
  Note:        0xff7043, // deep orange— annotations / assertions
  Topic:       0xe040fb, // magenta    — concepts / project types
  Dependency:  0x8bc34a, // lime       — packages / deps
  Command:     0xffd740, // gold       — shell commands
  Commit:      0xef5350, // red        — git commits
  Section:     0x29b6f6, // sky        — doc sections / headings
  Intent:      0xce93d8, // lavender   — intent / goals
  SubAgent:    0x80cbc4, // mint       — sub-agents
}

// CSS strings for links (8-digit hex = rgba shorthand, alpha ~25 %)
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

// Per-type icon shown in tooltip
const TYPE_ICON: Record<string, string> = {
  File: "📄", Directory: "📁", Symbol: "⚡", Task: "✅",
  URL: "🔗", Session: "🕐", Note: "📝", Topic: "🏷",
  Dependency: "📦", Command: "⌨", Commit: "🔀", Section: "§",
  Intent: "🎯", SubAgent: "🤖",
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
  onNodeClick?: (node: SWLNode | null) => void
}

// ── Component ─────────────────────────────────────────────────────────────────

export function SWLGraph({ data, hiddenTypes, onNodeClick }: Props) {
  const graphRef        = useRef<any>(null)
  const containerRef    = useRef<HTMLDivElement>(null)
  const bloomAdded      = useRef(false)
  const isFirstMount    = useRef(true)
  const [size, setSize] = useState({ w: 800, h: 600 })

  // Truth sources — updated by React Query refreshes and SSE deltas
  const allNodesRef = useRef<Map<string, SWLNode>>(
    new Map((data.nodes ?? []).map((n) => [n.id, n])),
  )
  const allLinksRef     = useRef<SWLLink[]>(data.links ?? [])
  const hiddenTypesRef  = useRef<Set<string>>(hiddenTypes ?? new Set())

  // Stable initial graphData reference — never changes, so ForceGraph3D's
  // prop comparison never triggers a simulation restart from the React side.
  const initialDataRef = useRef({
    nodes: data.nodes ?? [],
    links: data.links ?? [],
  })

  // ── applyFiltered ────────────────────────────────────────────────────────────
  // Full filtered rebuild; triggers a simulation restart (used for filter toggles
  // and periodic React Query refreshes where links may have changed).
  const applyFiltered = useCallback(() => {
    const fg = graphRef.current
    if (!fg) return
    const hidden = hiddenTypesRef.current
    const all    = Array.from(allNodesRef.current.values())
    const visible      = hidden.size === 0 ? all : all.filter((n) => !hidden.has(n.type))
    const visibleIds   = new Set(visible.map((n) => n.id))
    const visibleLinks = allLinksRef.current.filter((l) => {
      const src = typeof l.source === "object" ? (l.source as any).id : l.source
      const tgt = typeof l.target === "object" ? (l.target as any).id : l.target
      return visibleIds.has(src) && visibleIds.has(tgt)
    })
    fg.graphData({ nodes: visible, links: visibleLinks })
  }, [])

  // ── applySSEUpdate ───────────────────────────────────────────────────────────
  // Smart in-place update for SSE deltas: mutates existing node objects so the
  // force simulation keeps its current positions.  Only falls back to a full
  // graphData() call when genuinely new nodes arrive.
  const applySSEUpdate = useCallback(
    (updates: SWLNode[]) => {
      const fg = graphRef.current
      if (!fg) return
      const hidden = hiddenTypesRef.current

      // Persist into truth source first
      for (const n of updates) allNodesRef.current.set(n.id, n)

      const cur    = fg.graphData() as { nodes: any[]; links: any[] }
      const curMap = new Map<string, any>((cur.nodes ?? []).map((n: any) => [n.id, n]))

      const brandNew: SWLNode[] = []
      for (const n of updates) {
        if (hidden.has(n.type)) continue
        if (curMap.has(n.id)) {
          Object.assign(curMap.get(n.id), n) // mutate in-place → no restart
        } else {
          brandNew.push(n)
        }
      }

      if (brandNew.length > 0) {
        // New nodes require a graphData() call; existing positions are preserved
        // because we mutated the objects that are already in the simulation.
        applyFiltered()
      } else {
        fg.refresh?.() // re-render without touching the simulation
      }
    },
    [applyFiltered],
  )

  // ── React Query data refresh (new links / bulk re-sync) ───────────────────
  useEffect(() => {
    if (isFirstMount.current) {
      isFirstMount.current = false
      return // initial data already set as the graphData prop
    }
    for (const n of data.nodes ?? []) allNodesRef.current.set(n.id, n)
    if (data.links?.length) allLinksRef.current = data.links
    applyFiltered()
  }, [data, applyFiltered])

  // ── Filter changes ────────────────────────────────────────────────────────
  useEffect(() => {
    hiddenTypesRef.current = hiddenTypes ?? new Set()
    if (graphRef.current) applyFiltered()
  }, [hiddenTypes, applyFiltered])

  // ── SSE real-time updates ─────────────────────────────────────────────────
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

  // ── Container resize → width/height props ────────────────────────────────
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

  // ── Post-mount: bloom, orbit, zoom-to-fit, cursor override ───────────────
  useEffect(() => {
    let cancelled    = false
    let idleTimer: ReturnType<typeof setTimeout>

    const setup = () => {
      if (cancelled) return
      const fg = graphRef.current
      if (!fg) { requestAnimationFrame(setup); return }

      // Bloom ─────────────────────────────────────────────────────────────────
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

      // Auto-orbit ────────────────────────────────────────────────────────────
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

      // Remove pointer cursor ─────────────────────────────────────────────────
      if (canvas) {
        canvas.addEventListener(
          "mousemove",
          () => { canvas.style.cursor = "default" },
          { passive: true },
        )
      }

      // Zoom-to-fit after physics has had time to settle ──────────────────────
      setTimeout(() => { if (!cancelled) fg.zoomToFit(800, 60) }, 800)
    }

    requestAnimationFrame(setup)
    return () => {
      cancelled = true
      clearTimeout(idleTimer)
    }
  }, [])

  // ── THREE node objects ─────────────────────────────────────────────────────
  const buildNodeObject = useCallback((rawNode: any) => {
    const n     = rawNode as SWLNode
    const color = resolveColor(n)
    const r     = resolveRadius(n)

    if (n.factStatus === "stale" || n.factStatus === "deleted") {
      return new THREE.Mesh(
        new THREE.SphereGeometry(r, 8, 6),
        new THREE.MeshBasicMaterial({
          color,
          wireframe:   true,
          transparent: true,
          opacity:     n.factStatus === "deleted" ? 0.15 : 0.4,
        }),
      )
    }
    return new THREE.Mesh(
      new THREE.SphereGeometry(r, 14, 10),
      new THREE.MeshBasicMaterial({ color }),
    )
  }, [])

  // ── Link color ────────────────────────────────────────────────────────────
  const getLinkColor = useCallback(
    (link: any) => LINK_COLORS[(link as SWLLink).rel] ?? "#2a2d3a55",
    [],
  )

  // ── Tooltip ───────────────────────────────────────────────────────────────
  const getNodeLabel = useCallback((rawNode: any) => {
    const n       = rawNode as SWLNode
    const hexCol  = `#${(NODE_COLORS[n.type] ?? 0x888899).toString(16).padStart(6, "0")}`
    const icon    = TYPE_ICON[n.type] ?? "◉"
    const depth   = Math.min(n.knowledgeDepth ?? 0, 5)
    const depthBar = "█".repeat(depth) + "░".repeat(5 - depth)
    const statusColor =
      n.factStatus === "verified" ? "#3dd68c"
      : n.factStatus === "stale"  ? "#f5a623"
      : n.factStatus === "deleted"? "#ef5350"
      : "#5a6070"

    return `<div style="background:rgba(14,15,20,0.97);padding:9px 12px;border-radius:8px;font-family:monospace;font-size:11px;color:#a0a8bc;line-height:1.65;border:1px solid ${hexCol}44;box-shadow:0 0 16px ${hexCol}28,0 4px 12px rgba(0,0,0,0.8);max-width:300px">
  <div style="color:${hexCol};font-weight:700;font-size:12.5px;margin-bottom:5px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap">${icon}&nbsp;${n.name}</div>
  <div style="display:flex;gap:7px;font-size:10px;margin-bottom:7px;color:#5a6070">
    <span style="color:${hexCol}cc">${n.type}</span><span>·</span>
    <span style="color:${statusColor}">${n.factStatus}</span>
  </div>
  <div style="display:grid;grid-template-columns:80px 1fr;row-gap:3px;font-size:10px;column-gap:8px">
    <span style="color:#5a6070">confidence</span><span>${Math.round(n.confidence * 100)}%</span>
    <span style="color:#5a6070">depth</span><span style="color:#8bc34a;letter-spacing:2px">${depthBar}</span>
    <span style="color:#5a6070">accesses</span><span>${n.accessCount ?? 0}</span>
  </div>
</div>`
  }, [])

  // ── Click handlers ─────────────────────────────────────────────────────────
  const handleNodeClick = useCallback(
    (raw: any) => onNodeClick?.(raw as SWLNode),
    [onNodeClick],
  )
  const handleBgClick = useCallback(() => onNodeClick?.(null), [onNodeClick])

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
        graphData={initialDataRef.current as any}
        nodeId="id"
        nodeThreeObject={buildNodeObject}
        nodeThreeObjectExtend={false}
        nodeLabel={getNodeLabel}
        linkSource="source"
        linkTarget="target"
        linkColor={getLinkColor}
        linkWidth={0.6}
        linkOpacity={0.3}
        linkDirectionalArrowLength={3.5}
        linkDirectionalArrowRelPos={0.95}
        linkDirectionalParticles={2}
        linkDirectionalParticleWidth={1.5}
        linkDirectionalParticleColor={getLinkColor}
        linkDirectionalParticleSpeed={0.005}
        enableNodeDrag
        backgroundColor="#181818"
        showNavInfo={false}
        onNodeClick={handleNodeClick}
        onBackgroundClick={handleBgClick}
        warmupTicks={120}
        cooldownTime={5000}
        d3AlphaDecay={0.015}
        d3VelocityDecay={0.25}
      />
    </div>
  )
}
