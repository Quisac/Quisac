import { useEffect, useRef, useState, useCallback } from 'react'

const NODE_COLOR = {
  File: '#4361ee',
  Class: '#e37400',
  Function: '#188038',
  Method: '#b8960c',
  Module: '#8b5cf6',
  Variable: '#80868b',
}
const NODE_R = { File: 20, Class: 17, Function: 15, Method: 13, Module: 12, Variable: 10 }
const EDGE_COLOR = {
  Imports: '#4361ee',
  Contains: '#c7cedb',
  Calls: '#188038',
  Inherits: '#e37400',
  Instantiates: '#b8960c',
  Decorates: '#d93b8f',
}
const EDGE_DASH = { Contains: true }
const FILTER_TYPES = ['all', 'File', 'Class', 'Function', 'Method', 'Module', 'Variable']

export default function GraphCard() {
  const canvasRef = useRef(null)
  const wrapRef = useRef(null)
  const tooltipRef = useRef(null)

  const [graph, setGraph] = useState(null)
  const [loadErr, setLoadErr] = useState(null)
  const [filterType, setFilterType] = useState('all')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState(null)
  const [stats, setStats] = useState({ nodes: 0, edges: 0 })

  // mutable sim state that doesn't need re-renders
  const sim = useRef({
    zoom: 1,
    panX: 0,
    panY: 0,
    dragNode: null,
    panStart: null,
    dragMoved: false,
    focused: null,
    adj: {},
  })

  useEffect(() => {
    let cancelled = false
    fetch('/api/graph')
      .then((res) => {
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return res.json()
      })
      .then((data) => {
        if (cancelled) return
        setGraph(data)
        const adj = {}
        Object.values(data.nodes).forEach((n) => {
          adj[n.id] = { out: [], in: [] }
        })
        data.edges.forEach((e) => {
          if (adj[e.from]) adj[e.from].out.push(e)
          if (adj[e.to]) adj[e.to].in.push(e)
        })
        sim.current.adj = adj
        setStats({ nodes: Object.keys(data.nodes).length, edges: data.edges.length })
      })
      .catch((err) => {
        if (!cancelled) setLoadErr(err.message)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const visibleNodes = useCallback(() => {
    if (!graph) return []
    let ns = Object.values(graph.nodes)
    if (filterType !== 'all') ns = ns.filter((n) => n.type === filterType)
    const q = search.trim().toLowerCase()
    if (q) ns = ns.filter((n) => n.name.toLowerCase().includes(q) || n.id.toLowerCase().includes(q))
    return ns
  }, [graph, filterType, search])

  // init layout once graph loads
  useEffect(() => {
    if (!graph) return
    const byType = {}
    Object.values(graph.nodes).forEach((n) => {
      ;(byType[n.type] = byType[n.type] || []).push(n)
    })
    const types = Object.keys(byType)
    types.forEach((t, ti) => {
      const a0 = (ti / types.length) * Math.PI * 2
      const gr = 300
      const gcx = Math.cos(a0) * gr * 0.5
      const gcy = Math.sin(a0) * gr * 0.5
      byType[t].forEach((n, i) => {
        const a2 = (i / byType[t].length) * Math.PI * 2
        const r2 = gr * 0.35
        n.x = gcx + Math.cos(a2) * r2 + (Math.random() - 0.5) * 30
        n.y = gcy + Math.sin(a2) * r2 + (Math.random() - 0.5) * 30
        n.vx = 0
        n.vy = 0
      })
    })
  }, [graph])

  // main render/sim loop
  useEffect(() => {
    if (!graph) return
    const canvas = canvasRef.current
    const wrap = wrapRef.current
    const ctx = canvas.getContext('2d')
    let raf
    let W, H, dpr

    function resize() {
      dpr = window.devicePixelRatio || 1
      W = wrap.clientWidth
      H = wrap.clientHeight
      canvas.width = W * dpr
      canvas.height = H * dpr
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0)
    }
    resize()
    window.addEventListener('resize', resize)

    const allNodes = Object.values(graph.nodes)
    const allEdges = graph.edges

    function currentVisibleSet() {
      let ns = filterType === 'all' ? allNodes : allNodes.filter((n) => n.type === filterType)
      const q = search.trim().toLowerCase()
      if (q) ns = ns.filter((n) => n.name.toLowerCase().includes(q) || n.id.toLowerCase().includes(q))
      return new Set(ns.map((n) => n.id))
    }

    function focusNeighbors() {
      const f = sim.current.focused
      if (!f) return null
      const s = new Set([f])
      ;(sim.current.adj[f]?.out || []).forEach((e) => s.add(e.to))
      ;(sim.current.adj[f]?.in || []).forEach((e) => s.add(e.from))
      return s
    }

    function tick(vset) {
      const ns = allNodes.filter((n) => vset.has(n.id))
      const REP = 5000,
        SPRING = 0.005,
        IDEAL_CONTAINS = 70,
        IDEAL_OTHER = 180,
        DAMP = 0.82
      ns.forEach((a) => {
        ns.forEach((b) => {
          if (a === b) return
          const dx = a.x - b.x,
            dy = a.y - b.y,
            d2 = dx * dx + dy * dy || 1
          const f = REP / d2,
            d = Math.sqrt(d2)
          a.vx += (f * dx) / d
          a.vy += (f * dy) / d
        })
      })
      allEdges.forEach((e) => {
        const a = graph.nodes[e.from],
          b = graph.nodes[e.to]
        if (!a || !b || !vset.has(a.id) || !vset.has(b.id)) return
        const dx = b.x - a.x,
          dy = b.y - a.y,
          d = Math.sqrt(dx * dx + dy * dy) || 1
        const ideal = e.type === 'Contains' ? IDEAL_CONTAINS : IDEAL_OTHER
        const f = (d - ideal) * SPRING
        const fx = (f * dx) / d,
          fy = (f * dy) / d
        a.vx += fx
        a.vy += fy
        b.vx -= fx
        b.vy -= fy
      })
      ns.forEach((n) => {
        if (sim.current.dragNode === n) return
        n.vx *= DAMP
        n.vy *= DAMP
        n.x += n.vx
        n.y += n.vy
      })
    }

    function toWorld(sx, sy) {
      const { zoom, panX, panY } = sim.current
      return { x: (sx - W / 2) / zoom - panX, y: (sy - H / 2) / zoom - panY }
    }

    function drawArrow(x1, y1, x2, y2, r2, zoom) {
      const dx = x2 - x1,
        dy = y2 - y1,
        d = Math.sqrt(dx * dx + dy * dy) || 1
      const ux = dx / d,
        uy = dy / d
      const ex = x2 - ux * r2,
        ey = y2 - uy * r2
      const as = 6 / zoom
      ctx.beginPath()
      ctx.moveTo(ex, ey)
      ctx.lineTo(ex - as * (ux - uy * 0.4), ey - as * (uy + ux * 0.4))
      ctx.lineTo(ex - as * (ux + uy * 0.4), ey - as * (uy - ux * 0.4))
      ctx.closePath()
      ctx.fill()
    }

    function shortName(n) {
      return n.name.length > 16 ? n.name.slice(0, 15) + '…' : n.name
    }

    function draw(vset) {
      const { zoom, panX, panY, focused } = sim.current
      ctx.clearRect(0, 0, W, H)
      const focus = focusNeighbors()

      ctx.save()
      ctx.translate(W / 2, H / 2)
      ctx.scale(zoom, zoom)
      ctx.translate(panX, panY)

      allEdges.forEach((e) => {
        const a = graph.nodes[e.from],
          b = graph.nodes[e.to]
        if (!a || !b || !vset.has(a.id) || !vset.has(b.id)) return
        const faded = focus && !focus.has(a.id) && !focus.has(b.id)
        ctx.globalAlpha = faded ? 0.05 : 0.55
        ctx.strokeStyle = EDGE_COLOR[e.type] || '#c7cedb'
        ctx.lineWidth = (e.type === 'Contains' ? 1 : 1.5) / zoom
        ctx.setLineDash(EDGE_DASH[e.type] ? [5 / zoom, 3 / zoom] : [])
        const mx = (a.x + b.x) / 2 + (b.y - a.y) * 0.12,
          my = (a.y + b.y) / 2 + (a.x - b.x) * 0.12
        ctx.beginPath()
        ctx.moveTo(a.x, a.y)
        ctx.quadraticCurveTo(mx, my, b.x, b.y)
        ctx.stroke()
        ctx.setLineDash([])
        if (!faded && e.type !== 'Contains') {
          const r = NODE_R[b.type] || 12
          ctx.fillStyle = EDGE_COLOR[e.type] || '#c7cedb'
          const t = 0.75,
            ipx = mx + (b.x - mx) * t,
            ipy = my + (b.y - my) * t
          drawArrow(ipx, ipy, b.x, b.y, r, zoom)
        }
      })

      allNodes.forEach((n) => {
        if (!vset.has(n.id)) return
        const r = NODE_R[n.type] || 12
        const faded = focus && !focus.has(n.id)
        ctx.globalAlpha = faded ? 0.08 : 1
        ctx.fillStyle = NODE_COLOR[n.type] || '#80868b'
        ctx.beginPath()
        ctx.arc(n.x, n.y, r, 0, Math.PI * 2)
        ctx.fill()
        if (focused === n.id) {
          ctx.strokeStyle = '#1a1a2e'
          ctx.lineWidth = 2 / zoom
          ctx.beginPath()
          ctx.arc(n.x, n.y, r + 2, 0, Math.PI * 2)
          ctx.stroke()
        }
        ctx.globalAlpha = faded ? 0.08 : 1
        ctx.font = `600 10px system-ui,sans-serif`
        ctx.fillStyle = '#fff'
        ctx.textAlign = 'center'
        ctx.textBaseline = 'middle'
        ctx.fillText(shortName(n), n.x, n.y)
      })

      ctx.globalAlpha = 1
      ctx.restore()
    }

    function loop() {
      const vset = currentVisibleSet()
      tick(vset)
      draw(vset)
      raf = requestAnimationFrame(loop)
    }
    loop()

    function hitTest(sx, sy) {
      const { x, y } = toWorld(sx, sy)
      const vset = currentVisibleSet()
      let best = null,
        bd = Infinity
      allNodes.forEach((n) => {
        if (!vset.has(n.id)) return
        const r = (NODE_R[n.type] || 12) + 6
        const d = Math.hypot(n.x - x, n.y - y)
        if (d < r && d < bd) {
          bd = d
          best = n
        }
      })
      return best
    }

    function onMouseMove(e) {
      const r = canvas.getBoundingClientRect()
      const sx = e.clientX - r.left,
        sy = e.clientY - r.top
      const s = sim.current
      if (s.panStart) {
        s.panX += (sx - s.panStart.sx) / s.zoom
        s.panY += (sy - s.panStart.sy) / s.zoom
        s.panStart = { sx, sy }
        s.dragMoved = true
        return
      }
      if (s.dragNode) {
        const { x, y } = toWorld(sx, sy)
        s.dragNode.x = x
        s.dragNode.y = y
        s.dragNode.vx = 0
        s.dragNode.vy = 0
        s.dragMoved = true
        return
      }
      const hit = hitTest(sx, sy)
      const tip = tooltipRef.current
      if (hit) {
        tip.style.display = 'block'
        tip.style.left = sx + 14 + 'px'
        tip.style.top = sy - 10 + 'px'
        tip.textContent = `[${hit.type}] ${hit.name}`
        canvas.style.cursor = 'pointer'
      } else {
        tip.style.display = 'none'
        canvas.style.cursor = 'grab'
      }
    }

    function onMouseDown(e) {
      const s = sim.current
      s.dragMoved = false
      const r = canvas.getBoundingClientRect()
      const sx = e.clientX - r.left,
        sy = e.clientY - r.top
      const hit = hitTest(sx, sy)
      if (hit) {
        s.dragNode = hit
      } else {
        s.panStart = { sx, sy }
        canvas.classList.add('graph-panning')
      }
    }

    function onMouseUp() {
      const s = sim.current
      if (s.dragNode && !s.dragMoved) {
        s.focused = s.focused === s.dragNode.id ? null : s.dragNode.id
        setSelected(s.focused ? graph.nodes[s.focused] : null)
      }
      if (!s.dragNode && !s.dragMoved) {
        s.focused = null
        setSelected(null)
      }
      s.dragNode = null
      s.panStart = null
      canvas.classList.remove('graph-panning')
    }

    function onWheel(e) {
      e.preventDefault()
      const r = canvas.getBoundingClientRect()
      const sx = e.clientX - r.left,
        sy = e.clientY - r.top
      const s = sim.current
      const wBefore = toWorld(sx, sy)
      s.zoom *= e.deltaY < 0 ? 1.12 : 0.89
      s.zoom = Math.max(0.15, Math.min(6, s.zoom))
      const wAfter = toWorld(sx, sy)
      s.panX += wAfter.x - wBefore.x
      s.panY += wAfter.y - wBefore.y
    }

    canvas.addEventListener('mousemove', onMouseMove)
    canvas.addEventListener('mousedown', onMouseDown)
    canvas.addEventListener('mouseup', onMouseUp)
    canvas.addEventListener('wheel', onWheel, { passive: false })

    return () => {
      cancelAnimationFrame(raf)
      window.removeEventListener('resize', resize)
      canvas.removeEventListener('mousemove', onMouseMove)
      canvas.removeEventListener('mousedown', onMouseDown)
      canvas.removeEventListener('mouseup', onMouseUp)
      canvas.removeEventListener('wheel', onWheel)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [graph, filterType, search])

  useEffect(() => {
    setStats((s) => ({ ...s, nodes: visibleNodes().length }))
  }, [visibleNodes])

  function jumpTo(id) {
    if (!graph) return
    const target = graph.nodes[id]
    if (!target) return
    sim.current.focused = id
    setSelected(target)
  }

  return (
    <div className="graph-card">
      <div className="card-header">
        <span className="card-title">Codebase graph</span>
        <span className="card-meta">
          {stats.nodes} nodes · {stats.edges} edges
        </span>
      </div>

      <div className="graph-toolbar">
        <div className="graph-filters">
          {FILTER_TYPES.map((t) => (
            <button
              key={t}
              className={`gfbtn${filterType === t ? ' on' : ''}`}
              style={filterType === t && t !== 'all' ? { background: NODE_COLOR[t], borderColor: 'transparent', color: '#fff' } : undefined}
              onClick={() => setFilterType(t)}
            >
              {t === 'all' ? 'All' : t}
            </button>
          ))}
        </div>
        <input
          className="graph-search"
          placeholder="Filter by name…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>

      <div className="graph-main">
        <div className="graph-canvas-wrap" ref={wrapRef}>
          {loadErr ? (
            <div className="graph-error">
              <div className="graph-error-icon">⚠</div>
              <div className="graph-error-title">Could not load graph</div>
              <div className="graph-error-sub">{loadErr}</div>
            </div>
          ) : !graph ? (
            <div className="graph-loading">Loading graph…</div>
          ) : (
            <>
              <canvas ref={canvasRef} className="graph-canvas" />
              <div className="graph-hint">scroll to zoom · drag to pan · click node to inspect</div>
              <div className="graph-tooltip" ref={tooltipRef} />
            </>
          )}
        </div>

        <div className="graph-sidebar">
          {selected ? (
            <div className="graph-node-info">
              <span className="ni-type" style={{ background: NODE_COLOR[selected.type] || '#80868b' }}>
                {selected.type}
              </span>
              <div className="ni-name">{selected.name}</div>
              <div className="ni-id">{selected.id}</div>

              {(sim.current.adj[selected.id]?.out || []).length > 0 && (
                <>
                  <div className="ni-section">Outgoing ({sim.current.adj[selected.id].out.length})</div>
                  <div className="ni-list">
                    {sim.current.adj[selected.id].out.map((e, i) => (
                      <div className="ni-item" key={i} onClick={() => jumpTo(e.to)}>
                        <span className="ni-edge-type">{e.type}</span>
                        {graph.nodes[e.to]?.name || e.to}
                      </div>
                    ))}
                  </div>
                </>
              )}

              {(sim.current.adj[selected.id]?.in || []).length > 0 && (
                <>
                  <div className="ni-section">Incoming ({sim.current.adj[selected.id].in.length})</div>
                  <div className="ni-list">
                    {sim.current.adj[selected.id].in.map((e, i) => (
                      <div className="ni-item" key={i} onClick={() => jumpTo(e.from)}>
                        <span className="ni-edge-type">{e.type}</span>
                        {graph.nodes[e.from]?.name || e.from}
                      </div>
                    ))}
                  </div>
                </>
              )}
            </div>
          ) : (
            <div className="graph-legend-panel">
              <div className="leg-heading">Nodes</div>
              {Object.entries(NODE_COLOR).map(([type, color]) => (
                <div className="leg-row" key={type}>
                  <span className="leg-dot" style={{ background: color }} />
                  {type}
                </div>
              ))}
              <div className="leg-heading" style={{ marginTop: 12 }}>
                Edges
              </div>
              {Object.entries(EDGE_COLOR).map(([type, color]) => (
                <div className="leg-row" key={type}>
                  <span
                    className="leg-line"
                    style={
                      EDGE_DASH[type]
                        ? { borderTop: `2px dashed ${color}`, background: 'transparent', height: 0 }
                        : { background: color }
                    }
                  />
                  {type}
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
