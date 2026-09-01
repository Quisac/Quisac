import { useState, useCallback } from 'react'
import useIncidents, { useFaultGraph } from '../useIncidents'
import GraphCanvas from './GraphCanvas'

const URGENCY_COLOR = { high: 'badge-red', low: 'badge-amber' }

// How the pipeline's terminal states are presented. What matters on call is not
// "did it work" but "is there something to review, or something to do".
const RESOLUTION = {
  pending:   { label: 'Queued',      cls: 'res-pending'  },
  resolving: { label: 'Resolving…',  cls: 'res-working'  },
  resolved:  { label: 'Fix ready',   cls: 'res-resolved' },
  no_fix:    { label: 'Needs you',   cls: 'res-nofix'    },
  failed:    { label: 'Failed',      cls: 'res-failed'   },
}

const METHOD = {
  pattern: { label: 'Pattern match', hint: 'Reused a fix from a previous incident — no model call.' },
  llm:     { label: 'Model fix',     hint: 'Novel incident; the model wrote a patch over the files the graph selected.' },
}

function timeAgo(iso) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso)) / 1000)
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

/* ── Resolution ─────────────────────────────────────────────────────────── */

function ResolutionPanel({ res }) {
  if (!res || res.status === 'pending' || res.status === 'resolving') return null

  const method = METHOD[res.method]

  return (
    <div className={`res-panel ${res.status === 'resolved' ? 'ok' : ''}`}>
      <div className="res-panel-head">
        <span className="res-panel-title">
          {res.status === 'resolved' ? 'Fix ready for review' : 'Analysis'}
        </span>
        {method && (
          <span className="res-method" title={method.hint}>
            {method.label}
          </span>
        )}
        <span className={`res-tokens ${res.tokens === 0 ? 'zero' : ''}`}>
          {res.tokens === 0 ? '0 tokens' : `${res.tokens.toLocaleString()} tokens`}
        </span>
      </div>

      {res.root_cause && (
        <div className="res-block">
          <div className="res-block-label">Root cause</div>
          <p className="res-root-cause">{res.root_cause}</p>
        </div>
      )}

      {res.summary && (
        <div className="res-block">
          <div className="res-block-label">Proposed fix</div>
          <p className="res-summary">{res.summary}</p>
        </div>
      )}

      {res.files?.length > 0 && (
        <div className="res-block">
          <div className="res-block-label">Files changed</div>
          {res.files.map(f => (
            <div className="res-file" key={f.path}>
              <code>{f.path}</code>
              <span className="res-diff">
                {f.additions > 0 && <span className="diff diff-plus">+{f.additions}</span>}
                {f.deletions > 0 && <span className="diff diff-minus">−{f.deletions}</span>}
              </span>
            </div>
          ))}
        </div>
      )}

      {res.error && (
        <div className="res-block">
          <div className="res-block-label">
            {res.status === 'failed' ? 'What went wrong' : 'Why no patch was opened'}
          </div>
          <p className="res-reason">{res.error}</p>
        </div>
      )}

      {res.pr_url && (
        <a className="res-pr" href={res.pr_url} target="_blank" rel="noreferrer">
          <span className="res-pr-num">#{res.pr_number}</span>
          <span className="res-pr-branch">{res.branch}</span>
          <span className="res-pr-open">View pull request →</span>
        </a>
      )}

      {res.confidence > 0 && res.method === 'llm' && (
        <div className="res-confidence">
          Model confidence {Math.round(res.confidence * 100)}%
        </div>
      )}
    </div>
  )
}

/* ── Graph evidence ─────────────────────────────────────────────────────── */

function CandidateTable({ candidates }) {
  if (!candidates?.length) {
    return <p className="inc-empty">No candidates found in the indexed graph.</p>
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
            <th>Score</th>
            <th>Why it ranked here</th>
          </tr>
        </thead>
        <tbody>
          {candidates.map((c, i) => (
            <tr key={c.id} className={i === 0 ? 'top-candidate' : ''}>
              <td>{i + 1}</td>
              <td><code>{c.name}</code></td>
              <td><span className={`type-badge type-${c.type?.toLowerCase()}`}>{c.type}</span></td>
              <td className="file-cell"><code>{c.file_path || '—'}</code></td>
              <td>
                <span className="score-cell">
                  <span className="score-bar"><i style={{ width: `${Math.min(100, c.score * 100)}%` }} /></span>
                  {c.score?.toFixed(2)}
                </span>
              </td>
              <td className="reason-cell">{c.reasons?.join('; ') || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// FaultSubgraph draws every node on a path from a candidate back to the entry
// point. The table says which symbols are suspicious; this says how they reach
// the failure, which a list cannot express.
function FaultSubgraph({ incidentId }) {
  const { loading, graph, error } = useFaultGraph(incidentId)

  if (loading) return <p className="fg-state">Querying the graph…</p>
  if (error) return <p className="fg-state fg-error">Could not read the subgraph: {error}</p>
  if (!graph?.nodes?.length) {
    return <p className="fg-state">No stored traversal to draw — this incident was resolved without one.</p>
  }

  return (
    <>
      <div className="fault-graph">
        <GraphCanvas graph={graph} />
      </div>
      <div className="fg-meta">
        <span>{graph.nodes.length} nodes · {graph.edges.length} edges · shortest paths back to the entry point</span>
        {graph.truncated && (
          <span className="fg-truncated" title="The path scan hit its ceiling; some connections are not drawn.">
            partial
          </span>
        )}
      </div>
    </>
  )
}

/* ── One incident ───────────────────────────────────────────────────────── */

function IncidentCard({ inc, onRerun, onResolve }) {
  const [expanded, setExpanded] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  // Marking resolved deletes the incident, so the button arms before it acts.
  const [confirming, setConfirming] = useState(false)

  const res = inc.resolution ?? { status: 'pending', tokens: 0 }
  const state = RESOLUTION[res.status] ?? RESOLUTION.pending
  const working = res.status === 'resolving' || res.status === 'pending'

  const rerun = useCallback(async () => {
    setBusy(true)
    setError(null)
    try {
      const r = await fetch(`/api/incidents/${inc.id}/resolve`, {
        method: 'POST',
        credentials: 'same-origin',
      })
      if (!r.ok) throw new Error((await r.text()) || `HTTP ${r.status}`)
      onRerun()
    } catch (e) {
      setError(e.message)
    } finally {
      setBusy(false)
    }
  }, [inc.id, onRerun])

  const resolveAndDelete = useCallback(async () => {
    if (!confirming) {
      setConfirming(true)
      return
    }
    setBusy(true)
    setError(null)
    try {
      await onResolve(inc.id)
    } catch (e) {
      setError(e.message)
      setBusy(false)
      setConfirming(false)
    }
  }, [confirming, inc.id, onResolve])

  const hasAnalysis = inc.candidates?.length > 0 || inc.entry_point

  return (
    <div className={`incident-card ${res.status === 'resolved' ? 'traversed' : ''}`}>
      <div className="incident-header">
        <span className={`badge ${URGENCY_COLOR[inc.urgency] || 'badge-amber'}`}>
          {inc.urgency?.toUpperCase()}
        </span>
        <div className="incident-title-row">
          <span className="incident-title">{inc.title}</span>
          <span className="incident-time">{timeAgo(inc.received_at)}</span>
        </div>
      </div>

      <div className="incident-meta">
        <span className="inc-service">🔧 {inc.service_name}</span>
        {inc.repo && <span className="inc-repo">📦 {inc.repo}</span>}
        <span className={`res-chip ${state.cls}`}>
          {working && <span className="res-spinner" />}
          {state.label}
        </span>
        {res.resolved_at && <span className="inc-elapsed">in {timeAgo(inc.received_at)}</span>}
      </div>

      {inc.error_body && <pre className="incident-trace">{inc.error_body}</pre>}

      <ResolutionPanel res={res} />

      <div className="incident-actions">
        {hasAnalysis && (
          <button className="expand-btn" onClick={() => setExpanded(e => !e)}>
            {expanded ? '▲ Hide graph evidence' : '▼ Show graph evidence'}
          </button>
        )}
        {!working && (
          <button className="discover-btn" onClick={rerun} disabled={busy}>
            {busy ? '⏳ Queueing…' : '↻ Re-run pipeline'}
          </button>
        )}
        <button
          className={`resolve-btn ${confirming ? 'confirm' : ''}`}
          onClick={resolveAndDelete}
          onBlur={() => setConfirming(false)}
          disabled={busy}
          title="Close this incident out and remove it from the feed. The learned fix is kept."
        >
          {busy ? '⏳ Removing…' : confirming ? 'Confirm — delete incident' : '✓ Mark resolved'}
        </button>
        {error && <span className="inc-error">{error}</span>}
      </div>

      {expanded && hasAnalysis && (
        <div className="incident-result">
          {inc.entry_point && (
            <div className="entry-point-box">
              <span className="ep-label">Entry point</span>
              <code className="ep-name">{inc.entry_point.name}</code>
              <span className="ep-type">{inc.entry_point.type}</span>
              <code className="ep-id">{inc.entry_point.id}</code>
            </div>
          )}
          <h4 className="candidates-heading">Fault subgraph</h4>
          <FaultSubgraph incidentId={inc.id} />

          <h4 className="candidates-heading">Root-cause candidates</h4>
          <CandidateTable candidates={inc.candidates} />
        </div>
      )}
    </div>
  )
}

/* ── The view ───────────────────────────────────────────────────────────── */

export default function IncidentsView({ session }) {
  // Refreshes while anything is in flight, quiet once everything has settled.
  const { loading, incidents, error, reload, remove } = useIncidents()
  const [copied, setCopied] = useState(false)

  const webhookUrl = `${window.location.origin}/api/pagerduty/webhook?installation_id=${session?.installationId}`

  const copyWebhook = () => {
    navigator.clipboard.writeText(webhookUrl)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  const banner = (
    <div className="integration-banner">
      <div>
        <h4>PagerDuty webhook</h4>
        <p>Point your PagerDuty webhook here. Every alert is fingerprinted, traced through the code graph, and fixed if we can.</p>
        <code className="webhook-url">{webhookUrl}</code>
      </div>
      <button onClick={copyWebhook} className="connect-btn">
        {copied ? 'Copied' : 'Copy link'}
      </button>
    </div>
  )

  if (loading) return <div className="inc-loading">Loading incidents…</div>
  if (error) return <div className="inc-error-banner">Error: {error}</div>

  if (incidents.length === 0) {
    return (
      <div className="incidents-view">
        {banner}
        <div className="inc-empty-state">
          <div className="inc-empty-icon">🛡️</div>
          <h3>No incidents yet</h3>
          <p>When PagerDuty fires an alert, it is traced through your dependency graph and a fix is opened automatically.</p>
        </div>
      </div>
    )
  }

  return (
    <div className="incidents-view">
      {banner}
      <div className="incidents-list">
        {incidents.map(inc => (
          <IncidentCard key={inc.id} inc={inc} onRerun={reload} onResolve={remove} />
        ))}
      </div>
    </div>
  )
}
