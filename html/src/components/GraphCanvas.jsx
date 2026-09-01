import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ForceGraph2D from 'react-force-graph-2d'

const TYPE_COLOR = {
  File: '#3987e5',
  Class: '#e66767',
  Function: '#199e70',
  Method: '#c98500',
  Variable: '#9085e9',
}
const DEFAULT_COLOR = '#8a8f98'

// Roles a fault subgraph annotates its nodes with. The entry point gets its own
// colour: it is the one node that is not a hypothesis.
const ENTRY_COLOR = '#f2545b'
const ROLE_LEGEND = [
  ['entry', ENTRY_COLOR, 'Entry point'],
  ['candidate', '#e8a33d', 'Root-cause candidate'],
  ['link', DEFAULT_COLOR, 'On the path'],
]

// Node size carries rank; a node only holding the path together stays small.
function nodeRadius(node, isActive) {
  let r = 3.5
  if (node.role === 'entry') r = 6
  else if (node.role === 'candidate') r = 3.5 + 3 * Math.min(1, node.score ?? 0)
  else if (node.role === 'link') r = 2.4
  return isActive ? r + 2 : r
}

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
    // Absent on a repo index, where painting falls back to type colours.
    role: n.role,
    rank: n.rank ?? 0,
    score: n.score ?? 0,
    filePath: n.file_path,
    // External dependencies are not nodes; they ride along as raw import strings.
    external: n.type === 'File' ? raw?.raw_imports?.[n.name] : undefined,
    color: n.role === 'entry' ? ENTRY_COLOR : TYPE_COLOR[n.type] ?? DEFAULT_COLOR,
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
  const isFault = useMemo(() => data.nodes.some((n) => n.role), [data])

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
      const r = nodeRadius(node, isActive)

      if (isActive) {
        ctx.beginPath()
        ctx.arc(node.x, node.y, r + 4, 0, 2 * Math.PI)
        ctx.fillStyle = node.color + '33'
        ctx.fill()
      }

      // The entry point's ring keeps it findable at any zoom, and a candidate's
      // halo grows with its score, so the ranking reads off the shape.
      if (node.role === 'entry') {
        ctx.beginPath()
        ctx.arc(node.x, node.y, r + 3.5, 0, 2 * Math.PI)
        ctx.strokeStyle = ENTRY_COLOR + 'aa'
        ctx.lineWidth = 1.4 / scale
        ctx.stroke()
      } else if (node.role === 'candidate' && node.score > 0) {
        ctx.beginPath()
        ctx.arc(node.x, node.y, r + 2.5, 0, 2 * Math.PI)
        ctx.fillStyle = `rgba(232,163,61,${0.1 + 0.35 * Math.min(1, node.score)})`
        ctx.fill()
      }

      ctx.globalAlpha = node.role === 'link' ? 0.5 : 1
      ctx.beginPath()
      ctx.arc(node.x, node.y, r, 0, 2 * Math.PI)
      ctx.fillStyle = node.color
      ctx.fill()
      ctx.globalAlpha = 1

      // A whole repo index has too many nodes to label until zoom or hover.
      const alwaysLabel = node.role === 'entry' || node.rank === 1
      if (scale > 2.2 || isActive || alwaysLabel) {
        const fs = Math.max(11 / scale, 3)
        ctx.font = `600 ${fs}px ui-monospace, monospace`
        ctx.fillStyle = alwaysLabel ? 'rgba(255,255,255,0.98)' : 'rgba(233,238,247,0.92)'
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
        {(isFault
          ? ROLE_LEGEND.map(([role, color, label]) => [role, color, label])
          : Object.entries(TYPE_COLOR).map(([type, color]) => [type, color, type])
        ).map(([key, color, label]) => (
          <span className="gc-legend-item" key={key}>
            <span className="gc-legend-dot" style={{ background: color }} />
            {label}
          </span>
        ))}
      </div>

      {active && (
        <div className="gc-tooltip">
          <span className="gc-tip-type" style={{ color: active.color }}>{active.type}</span>
          <span className="gc-tip-name">{active.name}</span>
          {active.role === 'candidate' && (
            <span className="gc-tip-rank">#{active.rank} · score {active.score.toFixed(2)}</span>
          )}
          {active.role === 'entry' && <span className="gc-tip-rank">where the failure surfaced</span>}
          {active.filePath && <span className="gc-tip-id">{active.filePath}</span>}
          <span className="gc-tip-id">{active.id}</span>
          {active.external?.length > 0 && (
            <span className="gc-tip-ext">
              imports {active.external.slice(0, 5).join(', ')}
              {active.external.length > 5 && ` +${active.external.length - 5} more`}
            </span>
          )}
        </div>
      )}
    </div>
  )
}
