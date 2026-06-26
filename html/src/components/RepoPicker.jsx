import { useEffect, useMemo, useState } from 'react'

const connect = () => { window.location.href = '/api/connect' }
const configure = () => { window.location.href = '/api/install' }

export default function RepoPicker({ onSelect }) {
  const [state, setState] = useState({ status: 'loading', repos: [], error: null })
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(() => new Set())

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

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return state.repos
    return state.repos.filter((r) => r.full_name.toLowerCase().includes(q))
  }, [state.repos, query])

  const allShownSelected = filtered.length > 0 && filtered.every((r) => selected.has(r.id))

  const toggle = (id) => {
    setSelected((prev) => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      return next
    })
  }

  const toggleAllShown = () => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (allShownSelected) filtered.forEach((r) => next.delete(r.id))
      else filtered.forEach((r) => next.add(r.id))
      return next
    })
  }

  // ✅ FIXED: Get selected repo and call onSelect
  const importSelected = () => {
    const selectedRepos = state.repos.filter((r) => selected.has(r.id))
    if (selectedRepos.length === 0) return

    // Pass the first selected repo's full_name to parent
    const repoFullName = selectedRepos[0].full_name
    onSelect?.(repoFullName)
  }

  // ✅ ADDED: Quick index single repo directly
  const indexRepo = (repo) => {
    onSelect?.(repo.full_name)
  }

  return (
    <div className="card repo-picker">
      <div className="card-header">
        <span className="card-title">Import a repository</span>
        {state.status === 'ready' && (
          <button className="repo-config-link" onClick={configure}>Configure on GitHub ↗</button>
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
        <>
          <div className="repo-toolbar">
            <input
              className="repo-search"
              type="text"
              placeholder="Search repositories…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {filtered.length > 0 && (
              <label className="repo-selectall">
                <input type="checkbox" checked={allShownSelected} onChange={toggleAllShown} />
                Select all
              </label>
            )}
          </div>

          <div className="repo-list">
            {filtered.map((r) => (
              <label className="repo-row" key={r.id}>
                <input type="checkbox" checked={selected.has(r.id)} onChange={() => toggle(r.id)} />
                <span className="repo-name">{r.full_name}</span>
                <span className={`badge ${r.private ? 'badge-amber' : 'badge-green'}`}>
                  {r.private ? 'Private' : 'Public'}
                </span>
                {/* ✅ ADDED: Quick index button per repo */}
                <button
                  className="pr-btn repo-index-btn"
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    indexRepo(r)
                  }}
                >
                  Index
                </button>
              </label>
            ))}

            {filtered.length === 0 && state.repos.length > 0 && (
              <div className="repo-empty">No repositories match "{query}".</div>
            )}

            {state.repos.length === 0 && (
              <div className="repo-empty">
                No repositories granted yet.
                <button className="pr-btn repo-import" onClick={configure}>Add repositories</button>
              </div>
            )}
          </div>

          {state.repos.length > 0 && (
            <div className="repo-footer">
              <span className="repo-count">{selected.size} selected</span>
              <button
                className="connect-btn repo-import-btn"
                disabled={selected.size === 0}
                onClick={importSelected}
              >
                Import selected
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
