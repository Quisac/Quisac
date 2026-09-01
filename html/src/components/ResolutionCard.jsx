import useIncidents from '../useIncidents'

// The most recent incident the pipeline resolved, and how.

const METHOD_LABEL = {
  pattern: 'Pattern match — no model call',
  llm: 'Model fix over graph-selected files',
}

function relative(iso) {
  if (!iso) return ''
  const s = Math.floor((Date.now() - new Date(iso)) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`
  return `${Math.floor(s / 86400)}d ago`
}

export default function ResolutionCard() {
  const { loading, incidents } = useIncidents()

  if (loading) return null

  const latest = incidents.find((i) => i.resolution?.status === 'resolved')
  if (!latest) {
    return (
      <div className="res-card">
        <div className="res-header">
          <span className="res-title">Latest resolution</span>
        </div>
        <div className="res-body">
          <p className="res-empty">
            No incident has been resolved yet. When an alert fires, its resolution appears here.
          </p>
        </div>
      </div>
    )
  }

  const res = latest.resolution
  const files = res.files ?? []

  return (
    <div className="res-card">
      <div className="res-header">
        <span className="res-title">
          <span className="res-check">✓</span>
          Resolution — {latest.title}
        </span>
        <span className="badge badge-green">
          resolved · {res.method === 'pattern' ? 'pattern match' : 'model fix'}
        </span>
      </div>

      <div className="res-body">
        <div className="res-details">
          <div>
            <div className="res-group-label">Root cause</div>
            <div className="res-group-val">{res.summary || latest.service_name}</div>
            <div className="res-group-sub">{res.root_cause}</div>
          </div>
          <div>
            <div className="res-group-label">Resolution method</div>
            <div className="res-group-val">{METHOD_LABEL[res.method] ?? 'Automated'}</div>
            <div className="res-group-sub">
              {res.tokens === 0 ? '0 LLM tokens used' : `${res.tokens.toLocaleString()} LLM tokens used`}
              {' · '}{relative(res.resolved_at)}
            </div>
          </div>
        </div>

        {files.length > 0 && (
          <>
            <div className="files-label">Files identified by graph traversal</div>
            {files.map((f) => (
              <div className="file-row" key={f.path}>
                <span className="file-name">{f.path}</span>
                <span>
                  {f.additions > 0 && <span className="diff diff-plus">+{f.additions}</span>}{' '}
                  {f.deletions > 0 && <span className="diff diff-minus">−{f.deletions}</span>}
                </span>
              </div>
            ))}
          </>
        )}

        {res.pr_url && (
          <div className="pr-row">
            <div>
              <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
                <span className="pr-commit">fix: {res.summary}</span>
                <span className="badge-ready">ready to review</span>
              </div>
              <div className="pr-sub">
                opened by quisac · branch {res.branch} · {files.length} file{files.length === 1 ? '' : 's'} changed
              </div>
            </div>
            <a className="pr-btn" href={res.pr_url} target="_blank" rel="noreferrer">↑↓ View PR</a>
          </div>
        )}
      </div>
    </div>
  )
}
