import { useEffect, useState } from 'react'

// The learned-fix cache. Every row is an error shape already solved: the next
// incident that fingerprints to it is answered from these stored edits.

function timeAgo(iso) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso)) / 1000)
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

function usePatterns() {
  const [state, setState] = useState({ loading: true, patterns: [], error: null })

  // Patterns only change when a resolution finishes, which this view cannot
  // observe, so it loads once rather than polling.
  useEffect(() => {
    let cancelled = false
    ;(async () => {
      try {
        const res = await fetch('/api/patterns', { credentials: 'same-origin' })
        if (!res.ok) throw new Error((await res.text())?.trim() || `HTTP ${res.status}`)
        const patterns = (await res.json()) ?? []
        if (!cancelled) setState({ loading: false, patterns, error: null })
      } catch (e) {
        if (!cancelled) setState({ loading: false, patterns: [], error: String(e.message ?? e) })
      }
    })()
    return () => { cancelled = true }
  }, [])

  return state
}

function PatternCard({ p }) {
  const [open, setOpen] = useState(false)
  // An applied pattern is proven; a merely recorded one is a prediction.
  const proven = p.times_applied > 0

  return (
    <div className={`pattern-card ${proven ? 'proven' : ''}`}>
      <div className="pattern-head">
        <span className={`badge ${proven ? 'badge-green' : 'badge-blue'}`}>
          {proven ? `${p.times_applied}× applied` : 'Recorded'}
        </span>
        <div className="pattern-title-row">
          <span className="pattern-title">{p.root_cause || p.signature}</span>
          <span className="pattern-time">seen {timeAgo(p.last_seen_at)}</span>
        </div>
      </div>

      <div className="pattern-meta">
        {p.repo && <span className="inc-repo">📦 {p.repo}</span>}
        {p.service_name && <span className="inc-service">🔧 {p.service_name}</span>}
        <span className="pattern-seen">{p.times_seen}× seen</span>
        {proven && (
          <span
            className="pattern-saved"
            title="Incidents resolved from this pattern instead of a model call"
          >
            {p.times_applied} model call{p.times_applied === 1 ? '' : 's'} avoided
          </span>
        )}
      </div>

      {p.fix_summary && <p className="pattern-summary">{p.fix_summary}</p>}

      {p.files?.length > 0 && (
        <div className="pattern-files">
          {p.files.map(f => <code key={f}>{f}</code>)}
        </div>
      )}

      <button className="expand-btn" onClick={() => setOpen(o => !o)}>
        {open ? '▲ Hide signature' : '▼ Show signature'}
      </button>

      {open && (
        <div className="pattern-sig">
          <div className="res-block-label">Normalised signature</div>
          <pre>{p.signature}</pre>
          <div className="res-block-label">Fingerprint</div>
          <code className="pattern-fp">{p.fingerprint}</code>
        </div>
      )}
    </div>
  )
}

export default function PatternsView() {
  const { loading, patterns, error } = usePatterns()

  if (loading) return <div className="inc-loading">Loading patterns…</div>
  if (error) return <div className="inc-error-banner">Error: {error}</div>

  if (patterns.length === 0) {
    return (
      <div className="incidents-view">
        <div className="inc-empty-state">
          <div className="inc-empty-icon">🧠</div>
          <h3>No patterns learned yet</h3>
          <p>
            Every incident the model fixes is recorded against the shape of its error.
            When that shape fires again it is resolved from here — in milliseconds, with
            no model call.
          </p>
        </div>
      </div>
    )
  }

  const applied = patterns.reduce((n, p) => n + p.times_applied, 0)

  return (
    <div className="incidents-view">
      <div className="integration-banner">
        <div>
          <h4>Learned fixes</h4>
          <p>
            {patterns.length} error shape{patterns.length === 1 ? '' : 's'} recorded
            {applied > 0 && ` · ${applied} incident${applied === 1 ? '' : 's'} resolved from cache, with no model call`}.
          </p>
        </div>
      </div>
      <div className="incidents-list">
        {patterns.map(p => <PatternCard key={p.id} p={p} />)}
      </div>
    </div>
  )
}
