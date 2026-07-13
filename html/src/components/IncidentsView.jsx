import { useState, useEffect, useCallback } from 'react'

const URGENCY_COLOR = { high: 'badge-red', low: 'badge-amber' }

function timeAgo(iso) {
  const s = Math.floor((Date.now() - new Date(iso)) / 1000)
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

function CandidateTable({ candidates }) {
  if (!candidates || candidates.length === 0) {
    return <p className="inc-empty">No callers found in the indexed graph.</p>
  }
  return (
    <div className="candidates-wrap">
      <table className="candidates-table">
        <thead>
          <tr>
            <th>#</th>
            <th>Symbol</th>
            <th>Type</th>
            <th>File</th>
            <th>Depth</th>
            <th>Score</th>
          </tr>
        </thead>
        <tbody>
          {candidates.map((c, i) => (
            <tr key={c.id} className={i === 0 ? 'top-candidate' : ''}>
              <td>{i + 1}</td>
              <td><code>{c.name}</code></td>
              <td><span className={`type-badge type-${c.type?.toLowerCase()}`}>{c.type}</span></td>
              <td className="file-cell"><code>{c.file_path}</code></td>
              <td>{c.depth}</td>
              <td>{c.score?.toFixed(2)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

function IncidentCard({ inc }) {
  const [state, setState] = useState({
    loading: false,
    error: null,
    entry_point: inc.entry_point ?? null,
    candidates: inc.candidates ?? null,
    status: inc.status,
    repo: inc.repo ?? null,
  })
  const [expanded, setExpanded] = useState(false)

  const discover = useCallback(async () => {
    setState(s => ({ ...s, loading: true, error: null }))
    try {
      const res = await fetch(`/api/incidents/${inc.id}/traverse`, {
        method: 'POST',
        credentials: 'same-origin',
      })
      if (!res.ok) {
        const msg = await res.text()
        throw new Error(msg || `HTTP ${res.status}`)
      }
      const data = await res.json()
      setState(s => ({
        ...s,
        loading: false,
        status: 'traversed',
        repo: data.repo,
        entry_point: data.entry_point,
        candidates: data.candidates,
      }))
      setExpanded(true)
    } catch (e) {
      setState(s => ({ ...s, loading: false, error: e.message }))
    }
  }, [inc.id])

  const badgeClass = URGENCY_COLOR[inc.urgency] || 'badge-amber'

  return (
    <div className={`incident-card ${state.status === 'traversed' ? 'traversed' : ''}`}>
      <div className="incident-header">
        <span className={`badge ${badgeClass}`}>{inc.urgency?.toUpperCase()}</span>
        <div className="incident-title-row">
          <span className="incident-title">{inc.title}</span>
          <span className="incident-time">{timeAgo(inc.received_at)}</span>
        </div>
      </div>

      <div className="incident-meta">
        <span className="inc-service">🔧 {inc.service_name}</span>
        {state.repo && <span className="inc-repo">📦 {state.repo}</span>}
        <span className={`inc-status status-${state.status}`}>{state.status}</span>
      </div>

      {inc.error_body && (
        <pre className="incident-trace">{inc.error_body}</pre>
      )}

      <div className="incident-actions">
        {state.status !== 'traversed' && (
          <button
            className="discover-btn"
            onClick={discover}
            disabled={state.loading || state.status === 'traversing'}
          >
            {state.loading || state.status === 'traversing'
              ? '⏳ Traversing graph…'
              : '🔍 Discover Root Cause'}
          </button>
        )}

        {state.status === 'traversed' && (
          <button className="expand-btn" onClick={() => setExpanded(e => !e)}>
            {expanded ? '▲ Hide Analysis' : '▼ Show Analysis'}
          </button>
        )}

        {state.error && <span className="inc-error">{state.error}</span>}
      </div>

      {expanded && state.status === 'traversed' && (
        <div className="incident-result">
          {state.entry_point && (
            <div className="entry-point-box">
              <span className="ep-label">Entry Point</span>
              <code className="ep-name">{state.entry_point.name}</code>
              <span className="ep-type">{state.entry_point.type}</span>
              <code className="ep-id">{state.entry_point.id}</code>
            </div>
          )}
          <h4 className="candidates-heading">Root Cause Candidates</h4>
          <CandidateTable candidates={state.candidates} />
        </div>
      )}
    </div>
  )
}

export default function IncidentsView({ session }) {
  const [state, setState] = useState({ loading: true, incidents: [], error: null })

  useEffect(() => {
    let cancelled = false
    fetch('/api/incidents', { credentials: 'same-origin' })
      .then(r => r.ok ? r.json() : Promise.reject(`HTTP ${r.status}`))
      .then(incidents => { if (!cancelled) setState({ loading: false, incidents: incidents ?? [], error: null }) })
      .catch(e => { if (!cancelled) setState({ loading: false, incidents: [], error: String(e) }) })
    return () => { cancelled = true }
  }, [])

  const webhookUrl = `${window.location.origin}/api/pagerduty/webhook?installation_id=${session?.installationId}`

  const copyWebhook = () => {
    navigator.clipboard.writeText(webhookUrl)
  }

  if (state.loading) return <div className="inc-loading">Loading incidents…</div>
  if (state.error) return <div className="inc-error-banner">Error: {state.error}</div>

  const integrationBanner = (
    <div className="integration-banner" style={{ background: 'var(--bg)', padding: '1rem', borderRadius: '8px', border: '1px solid var(--border)', marginBottom: '1.5rem', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
      <div>
        <h4 style={{ margin: '0 0 0.5rem 0', fontSize: '14px' }}>PagerDuty Webhook Integration</h4>
        <p style={{ margin: 0, fontSize: '13px', color: 'var(--muted)' }}>
          Configure your PagerDuty webhook to point to this URL:
        </p>
        <code style={{ display: 'inline-block', marginTop: '0.5rem', padding: '4px 8px', background: 'var(--bg-hover)', borderRadius: '4px', fontSize: '12px', color: 'var(--blue)', wordBreak: 'break-all' }}>
          {webhookUrl}
        </code>
      </div>
      <button onClick={copyWebhook} className="connect-btn" style={{ marginLeft: '1rem', whiteSpace: 'nowrap' }}>Copy Link</button>
    </div>
  )

  if (state.incidents.length === 0) {
    return (
      <div className="incidents-view">
        {integrationBanner}
        <div className="inc-empty-state">
          <div className="inc-empty-icon">🛡️</div>
          <h3>No incidents yet</h3>
          <p>When PagerDuty fires an alert, it will automatically be traversed and appear here.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="incidents-view">
      {integrationBanner}
      <div className="incidents-list">
        {state.incidents.map(inc => <IncidentCard key={inc.id} inc={inc} />)}
      </div>
    </div>
  )
}
