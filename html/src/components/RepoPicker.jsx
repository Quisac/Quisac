import { useEffect, useState } from 'react'

const connect = () => { window.location.href = '/api/connect' }

// RepoPicker fetches the repositories granted to the GitHub App installation
// and lists them for import, Vercel-style. When no session exists yet
// (/api/repos returns 401) it prompts the user to connect.
export default function RepoPicker() {
  const [state, setState] = useState({ status: 'loading', repos: [], error: null })

  useEffect(() => {
    let cancelled = false
    fetch('/api/repos', { credentials: 'same-origin' })
      .then(async (res) => {
        if (res.status === 401) return { status: 'unauth' }
        if (!res.ok) throw new Error(`HTTP ${res.status}`)
        return { status: 'ready', repos: await res.json() }
      })
      .then((data) => { if (!cancelled) setState({ repos: [], error: null, ...data }) })
      .catch((err) => { if (!cancelled) setState({ status: 'error', repos: [], error: err.message }) })
    return () => { cancelled = true }
  }, [])

  return (
    <div className="card repo-picker">
      <div className="card-header">
        <span className="card-title">Import a repository</span>
        {state.status === 'ready' && (
          <span className="card-meta">{state.repos.length} available</span>
        )}
      </div>

      {state.status === 'loading' && (
        <div className="repo-empty">Loading your repositories…</div>
      )}

      {state.status === 'unauth' && (
        <div className="repo-empty">
          Connect your GitHub account to import repositories.
          <button className="pr-btn repo-import" onClick={connect}>Connect your repo</button>
        </div>
      )}

      {state.status === 'error' && (
        <div className="repo-empty">Could not load repositories ({state.error}).</div>
      )}

      {state.status === 'ready' && (
        <div className="repo-list">
          {state.repos.map((r) => (
            <div className="repo-row" key={r.id}>
              <span className="repo-name">{r.full_name}</span>
              <span className={`badge ${r.private ? 'badge-amber' : 'badge-green'}`}>
                {r.private ? 'Private' : 'Public'}
              </span>
              <button className="pr-btn repo-import">Import</button>
            </div>
          ))}
          {state.repos.length === 0 && (
            <div className="repo-empty">
              No repositories granted.
              <button className="pr-btn repo-import" onClick={connect}>Adjust access</button>
            </div>
          )}
        </div>
      )}
    </div>
  )
}
