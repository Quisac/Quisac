import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ForceGraph2D from 'react-force-graph-2d'

const TYPE_COLOR = {
  File: '#3987e5',
  Class: '#e66767',
  Function: '#199e70',
  Method: '#c98500',
  Variable: '#9085e9',
  Module: '#8a8f98',
}
const DEFAULT_COLOR = '#8a8f98'

const LINK_ALPHA = {
  Imports: 0.5,
  Contains: 0.16,
  Calls: 0.42,
  Instantiates: 0.34,
  Inherits: 0.55,
  Decorates: 0.34,
}

function toForceGraph(raw) {
  const nodes = Object.values(raw?.nodes ?? {}).map((n) => ({
    id: n.id,
    name: n.name,
    type: n.type,
    color: TYPE_COLOR[n.type] ?? DEFAULT_COLOR,
  }))
  const links = (raw?.edges ?? []).map((e) => ({
    source: e.from,
    target: e.to,
    edgeType: e.type,
  }))
  return { nodes, links }
}

export default function GraphCanvas({ graph }) {
  const data = useMemo(() => toForceGraph(graph), [graph])

  const wrapRef = useRef(null)
  const fgRef = useRef(null)
  const [dims, setDims] = useState({ w: 800, h: 520 })
  const [hovered, setHovered] = useState(null)
  const [pinned, setPinned] = useState(null)

  useEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const ro = new ResizeObserver(([entry]) => {
      setDims({ w: entry.contentRect.width, h: entry.contentRect.height })
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  const active = hovered || pinned

  const paintNode = useCallback(
    (node, ctx, scale) => {
      const isActive = active && active.id === node.id
      const r = isActive ? 5.5 : 3.5

      if (isActive) {
        ctx.beginPath()
        ctx.arc(node.x, node.y, r + 4, 0, 2 * Math.PI)
        ctx.fillStyle = node.color + '33'
        ctx.fill()
      }

      ctx.beginPath()
      ctx.arc(node.x, node.y, r, 0, 2 * Math.PI)
      ctx.fillStyle = node.color
      ctx.fill()

      if (scale > 2.2 || isActive) {
        const fs = Math.max(11 / scale, 3)
        ctx.font = `600 ${fs}px ui-monospace, monospace`
        ctx.fillStyle = 'rgba(233,238,247,0.92)'
        ctx.textAlign = 'center'
        ctx.textBaseline = 'top'
        ctx.fillText(node.name, node.x, node.y + r + 2)
      }
    },
    [active],
  )

  return (
    <div className="gc-canvas" ref={wrapRef}>
      <ForceGraph2D
        ref={fgRef}
        width={dims.w}
        height={dims.h}
        graphData={data}
        backgroundColor="rgba(0,0,0,0)"
        nodeRelSize={4}
        nodeCanvasObject={paintNode}
        nodePointerAreaPaint={(node, color, ctx) => {
          ctx.beginPath()
          ctx.arc(node.x, node.y, 7, 0, 2 * Math.PI)
          ctx.fillStyle = color
          ctx.fill()
        }}
        onNodeHover={setHovered}
        onNodeClick={(node) => {
          setPinned(node)
          fgRef.current?.centerAt(node.x, node.y, 500)
          fgRef.current?.zoom(4, 500)
        }}
        onBackgroundClick={() => setPinned(null)}
        linkColor={(l) => `rgba(148,163,184,${LINK_ALPHA[l.edgeType] ?? 0.25})`}
        linkWidth={(l) => (active && (l.source.id === active.id || l.target.id === active.id) ? 1.4 : 0.5)}
        linkDirectionalParticles={0}
        cooldownTicks={140}
        warmupTicks={24}
      />

      <div className="gc-legend">
        {Object.entries(TYPE_COLOR).map(([type, color]) => (
          <span className="gc-legend-item" key={type}>
            <span className="gc-legend-dot" style={{ background: color }} />
            {type}
          </span>
        ))}
      </div>

      {active && (
        <div className="gc-tooltip">
          <span className="gc-tip-type" style={{ color: active.color }}>{active.type}</span>
          <span className="gc-tip-name">{active.name}</span>
          <span className="gc-tip-id">{active.id}</span>
        </div>
      )}
    </div>
  )
}
