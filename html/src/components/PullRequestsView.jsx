import useIncidents from '../useIncidents'

// Every pull request the pipeline has opened, newest first.
//
// There is no separate PR store: this reads the incident feed and keeps the rows
// that shipped. So closing an incident out drops its PR from this list — the
// pull request is untouched on GitHub, and the fix survives in Patterns.

const METHOD = {
  pattern: { label: 'From cache', cls: 'badge-green', hint: 'Reused a recorded fix — no model call.' },
  llm:     { label: 'Model fix',  cls: 'badge-blue',  hint: 'Novel incident; the model wrote the patch.' },
}

function timeAgo(iso) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso)) / 1000)
  if (s < 60) return `${s}s ago`
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

function PullRequestCard({ inc }) {
  const res = inc.resolution
  const method = METHOD[res.method]
  const files = res.files ?? []
  const additions = files.reduce((n, f) => n + (f.additions ?? 0), 0)
  const deletions = files.reduce((n, f) => n + (f.deletions ?? 0), 0)

  return (
    <div className="pr-card">
      <div className="pr-head">
        <a className="pr-num" href={res.pr_url} target="_blank" rel="noreferrer">
          #{res.pr_number}
        </a>
        <div className="pr-title-row">
          <span className="pr-title">{inc.title}</span>
          <span className="pattern-time">{timeAgo(res.resolved_at)}</span>
        </div>
      </div>

      <div className="pr-meta">
        {inc.repo && <span className="inc-repo">📦 {inc.repo}</span>}
        {res.branch && <code className="pr-branch">{res.branch}</code>}
        {method && <span className={`badge ${method.cls}`} title={method.hint}>{method.label}</span>}
        <span className={`res-tokens ${res.tokens === 0 ? 'zero' : ''}`}>
          {res.tokens === 0 ? '0 tokens' : `${res.tokens.toLocaleString()} tokens`}
        </span>
        {(additions > 0 || deletions > 0) && (
          <span className="res-diff">
            {additions > 0 && <span className="diff diff-plus">+{additions}</span>}
            {deletions > 0 && <span className="diff diff-minus">−{deletions}</span>}
          </span>
        )}
      </div>

      {res.summary && <p className="pattern-summary">{res.summary}</p>}

      {files.length > 0 && (
        <div className="pattern-files">
          {files.map(f => <code key={f.path}>{f.path}</code>)}
        </div>
      )}

      <a className="res-pr" href={res.pr_url} target="_blank" rel="noreferrer">
        <span className="res-pr-open">View pull request →</span>
      </a>
    </div>
  )
}

export default function PullRequestsView() {
  const { loading, incidents, error } = useIncidents()

  if (loading) return <div className="inc-loading">Loading pull requests…</div>
  if (error) return <div className="inc-error-banner">Error: {error}</div>

  // By when the fix landed, not when the alert arrived.
  const prs = incidents
    .filter(inc => inc.resolution?.pr_url)
    .sort((a, b) =>
      new Date(b.resolution.resolved_at ?? b.received_at) -
      new Date(a.resolution.resolved_at ?? a.received_at))

  if (prs.length === 0) {
    return (
      <div className="incidents-view">
        <div className="inc-empty-state">
          <div className="inc-empty-icon">🔀</div>
          <h3>No pull requests yet</h3>
          <p>
            When an incident is resolved, the patch is pushed to a branch and opened as a
            draft pull request. Those show up here.
          </p>
        </div>
      </div>
    )
  }

  const fromCache = prs.filter(p => p.resolution.method === 'pattern').length

  return (
    <div className="incidents-view">
      <div className="integration-banner">
        <div>
          <h4>Opened pull requests</h4>
          <p>
            {prs.length} pull request{prs.length === 1 ? '' : 's'} opened from incidents
            {fromCache > 0 && ` · ${fromCache} written from the pattern cache with no model call`}.
            Closing an incident out removes it from this list; the pull request stays on GitHub.
          </p>
        </div>
      </div>
      <div className="incidents-list">
        {prs.map(inc => <PullRequestCard key={inc.id} inc={inc} />)}
      </div>
    </div>
  )
}
