import { useCallback, useEffect, useRef, useState } from "react"
import ForceGraph3D from "react-force-graph-3d"
import * as THREE from "three"

import { type SWLGraphData, type SWLLink, type SWLNode, swlApi } from "@/api/swl"

// Hex colors per entity type (number for THREE materials, string for links)
const NODE_COLOR_HEX: Record<string, number> = {
  File: 0x4499ff,
  Directory: 0x778899,
  Symbol: 0x44ff99,
  Task: 0xffcc33,
  URL: 0x33ffee,
  Session: 0xcc44ff,
  Note: 0xff8833,
  Topic: 0xff3388,
  Dependency: 0x88ff33,
  Command: 0xffee33,
  Commit: 0xff4444,
  Section: 0x33ccff,
  Intent: 0xff88cc,
  SubAgent: 0x88ccff,
}

const LINK_COLOR: Record<string, string> = {
  defines: "#44ff99",
  imports: "#88ff44",
  has_task: "#ffcc33",
  has_section: "#33ccff",
  in_dir: "#2a2a44",
  written_in: "#4499ff",
  edited_in: "#4499ff",
  read: "#9999ff",
  executed: "#ffee33",
  fetched: "#33ffee",
  spawned_by: "#cc44ff",
  intended_for: "#ff88cc",
}

function resolveNodeColor(n: SWLNode): number {
  if (n.factStatus === "deleted") return 0x1a1a2e
  if (n.factStatus === "stale") return 0x2a2a44
  return NODE_COLOR_HEX[n.type] ?? 0xffffff
}

function resolveNodeRadius(n: SWLNode): number {
  return 2.5 + Math.cbrt(n.accessCount ?? 0) * 1.8 + n.knowledgeDepth * 0.5
}

interface Props {
  data: SWLGraphData
  onNodeClick?: (node: SWLNode | null) => void
}

export function SWLGraph({ data, onNodeClick }: Props) {
  const graphRef = useRef<any>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const bloomAdded = useRef(false)
  const [size, setSize] = useState({ w: 800, h: 600 })

  // Freeze initial data in a ref — SSE pushes updates imperatively so React
  // never needs to re-render this component just because graph data changes.
  const initialData = useRef({
    nodes: data.nodes ?? [],
    links: data.links ?? [],
  })

  // ── SSE real-time updates ──────────────────────────────────────────────────
  // We use the imperative `graphRef.current.graphData()` setter so the force
  // simulation is not fully restarted (avoiding the "whole-screen flicker").
  useEffect(() => {
    const es = new EventSource(swlApi.streamUrl())
    let debounceTimer: ReturnType<typeof setTimeout>

    es.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data) as { type: string; nodes: SWLNode[] }
        if (msg.type !== "delta" || !msg.nodes?.length) return

        clearTimeout(debounceTimer)
        debounceTimer = setTimeout(() => {
          const fg = graphRef.current
          if (!fg) return
          const cur = fg.graphData() as { nodes: SWLNode[]; links: SWLLink[] }
          const map = new Map((cur.nodes ?? []).map((n: SWLNode) => [n.id, n]))
          for (const n of msg.nodes) map.set(n.id, n)
          fg.graphData({ nodes: Array.from(map.values()), links: cur.links ?? [] })
        }, 800)
      } catch {
        // ignore malformed SSE events
      }
    }

    return () => {
      clearTimeout(debounceTimer)
      es.close()
    }
  }, [])

  // ── Container resize → update width/height props ──────────────────────────
  // width/height are React props on ForceGraph3D (not imperative methods).
  // Changing them does NOT restart the physics simulation, so no flicker.
  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    const applySize = () => {
      const w = el.clientWidth
      const h = el.clientHeight
      if (w > 0 && h > 0) setSize({ w, h })
    }

    applySize()
    const ro = new ResizeObserver(applySize)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  // ── Bloom post-processing ──────────────────────────────────────────────────
  // Attempt to add UnrealBloomPass after the first animation frame so the
  // THREE.js renderer and EffectComposer are guaranteed to exist.
  useEffect(() => {
    let cancelled = false

    const tryAddBloom = () => {
      if (cancelled || bloomAdded.current) return
      const fg = graphRef.current
      if (!fg) {
        requestAnimationFrame(tryAddBloom)
        return
      }
      const composer = fg.postProcessingComposer?.()
      if (!composer) {
        requestAnimationFrame(tryAddBloom)
        return
      }

      // Try new 'addons' alias, fall back to legacy 'examples/jsm' path
      const load = () =>
        import("three/addons/postprocessing/UnrealBloomPass.js" as string).catch(() =>
          import("three/examples/jsm/postprocessing/UnrealBloomPass.js" as string),
        )

      load()
        .then(({ UnrealBloomPass }: any) => {
          if (cancelled) return
          const el = containerRef.current
          const w = el?.clientWidth ?? 800
          const h = el?.clientHeight ?? 600
          const bp = new UnrealBloomPass(new THREE.Vector2(w, h), 1.8, 0.6, 0)
          composer.addPass(bp)
          bloomAdded.current = true
        })
        .catch(() => {
          // Bloom unavailable — graph still works without it
        })
    }

    requestAnimationFrame(tryAddBloom)
    return () => {
      cancelled = true
    }
  }, [])

  // ── Node THREE object (glowing sphere) ─────────────────────────────────────
  const buildNodeObject = useCallback((rawNode: any) => {
    const n = rawNode as SWLNode
    const color = resolveNodeColor(n)
    const r = resolveNodeRadius(n)
    const opacity =
      n.factStatus === "deleted" ? 0.12 : n.factStatus === "stale" ? 0.35 : 1.0
    return new THREE.Mesh(
      new THREE.SphereGeometry(r, 14, 10),
      new THREE.MeshBasicMaterial({
        color,
        transparent: opacity < 1,
        opacity,
      }),
    )
  }, [])

  // ── Link color ─────────────────────────────────────────────────────────────
  const getLinkColor = useCallback(
    (link: any) => LINK_COLOR[(link as SWLLink).rel] ?? "#1a1a3a",
    [],
  )

  // ── Node tooltip (HTML) ────────────────────────────────────────────────────
  const getNodeLabel = useCallback((rawNode: any) => {
    const n = rawNode as SWLNode
    return `<div style="background:rgba(5,5,20,0.9);padding:5px 9px;border-radius:5px;font-family:monospace;font-size:11px;color:#ccd;line-height:1.5;border:1px solid rgba(100,100,200,0.25)">
      <b style="color:#fff;font-size:12px">${n.name}</b><br/>
      <span style="color:#88aaff">${n.type}</span>
      <span style="opacity:0.5;margin:0 4px">·</span>
      <span style="opacity:0.6">${Math.round(n.confidence * 100)}% conf</span>
      <span style="opacity:0.5;margin:0 4px">·</span>
      <span style="opacity:0.55">${n.factStatus}</span>
    </div>`
  }, [])

  // ── Node / background click ────────────────────────────────────────────────
  const handleNodeClick = useCallback(
    (rawNode: any) => onNodeClick?.(rawNode as SWLNode),
    [onNodeClick],
  )
  const handleBgClick = useCallback(() => onNodeClick?.(null), [onNodeClick])

  return (
    <div
      ref={containerRef}
      style={{ width: "100%", height: "100%", background: "#050510" }}
    >
      <ForceGraph3D
        ref={graphRef}
        width={size.w}
        height={size.h}
        graphData={initialData.current as any}
        nodeId="id"
        nodeThreeObject={buildNodeObject}
        nodeThreeObjectExtend={false}
        nodeLabel={getNodeLabel}
        linkSource="source"
        linkTarget="target"
        linkColor={getLinkColor}
        linkWidth={0.5}
        linkOpacity={0.25}
        linkDirectionalArrowLength={3.5}
        linkDirectionalArrowRelPos={0.95}
        linkDirectionalParticles={2}
        linkDirectionalParticleWidth={1.5}
        linkDirectionalParticleColor={getLinkColor}
        linkDirectionalParticleSpeed={0.005}
        enableNodeDrag
        backgroundColor="#050510"
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
