import { useEffect, useMemo, useState } from 'react'

const connect = () => { window.location.href = '/api/connect' }
const configure = () => { window.location.href = '/api/install' }

// "owner/repo" → { owner: "owner", name: "repo" }. Falls back gracefully if the
// shape is ever unexpected so a row never renders blank.
const splitName = (fullName) => {
  const i = fullName.lastIndexOf('/')
  if (i === -1) return { owner: '', name: fullName }
  return { owner: fullName.slice(0, i), name: fullName.slice(i + 1) }
}

const LockIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
    <rect x="3" y="7" width="10" height="6.5" rx="1.5" stroke="currentColor" strokeWidth="1.4" />
    <path d="M5.25 7V5.25a2.75 2.75 0 0 1 5.5 0V7" stroke="currentColor" strokeWidth="1.4" />
  </svg>
)

const GlobeIcon = () => (
  <svg width="12" height="12" viewBox="0 0 16 16" fill="none" aria-hidden="true">
    <circle cx="8" cy="8" r="5.5" stroke="currentColor" strokeWidth="1.4" />
    <path d="M2.5 8h11M8 2.5c1.6 1.6 1.6 9.4 0 11M8 2.5C6.4 4.1 6.4 11.9 8 13.5"
      stroke="currentColor" strokeWidth="1.4" />
  </svg>
)

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

  const importSelected = () => {
    const first = state.repos.find((r) => selected.has(r.id))
    if (first) onSelect?.(first.full_name)
  }

  const indexRepo = (repo) => { onSelect?.(repo.full_name) }

  return (
    <div className="card repo-picker">
      <div className="card-header">
        <div className="repo-heading">
          <span className="card-title">Index a repository</span>
          {state.status === 'ready' && (
            <span className="repo-subtitle">
              {state.repos.length} {state.repos.length === 1 ? 'repository' : 'repositories'} available
            </span>
          )}
        </div>
        {state.status === 'ready' && (
          <button className="repo-config-link" onClick={configure}>Manage access ↗</button>
        )}
      </div>

      {state.status === 'loading' && (
        <div className="repo-list">
          {[0, 1, 2].map((i) => (
            <div className="repo-row repo-row-skeleton" key={i}>
              <span className="repo-skel repo-skel-check" />
              <span className="repo-skel repo-skel-name" />
              <span className="repo-skel repo-skel-chip" />
            </div>
          ))}
        </div>
      )}

      {state.status === 'unauth' && (
        <div className="repo-empty">
          <p className="repo-empty-text">Connect your GitHub account to index repositories.</p>
          <button className="connect-btn" onClick={connect}>Connect GitHub</button>
        </div>
      )}

      {state.status === 'error' && (
        <div className="repo-empty">
          <p className="repo-empty-text">Couldn’t load repositories — {state.error}.</p>
        </div>
      )}

      {state.status === 'ready' && (
        <>
          <div className="repo-toolbar">
            <div className="repo-search-wrap">
              <svg className="repo-search-icon" width="15" height="15" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
                <path d="m11 11 3 3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
              </svg>
              <input
                className="repo-search"
                type="text"
                placeholder="Search repositories…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </div>
            {filtered.length > 0 && (
              <label className="repo-selectall">
                <input type="checkbox" checked={allShownSelected} onChange={toggleAllShown} />
                Select all
              </label>
            )}
          </div>

          <div className="repo-list">
            {filtered.map((r) => {
              const { owner, name } = splitName(r.full_name)
              const isSelected = selected.has(r.id)
              return (
                <label className={`repo-row${isSelected ? ' is-selected' : ''}`} key={r.id}>
                  <input
                    className="repo-check"
                    type="checkbox"
                    checked={isSelected}
                    onChange={() => toggle(r.id)}
                  />
                  <span className="repo-name">
                    {owner && <span className="repo-owner">{owner}/</span>}
                    <span className="repo-repo">{name}</span>
                  </span>
                  <span className={`repo-vis ${r.private ? 'repo-vis-private' : 'repo-vis-public'}`}>
                    {r.private ? <LockIcon /> : <GlobeIcon />}
                    {r.private ? 'Private' : 'Public'}
                  </span>
                  <button
                    className="repo-index-btn"
                    onClick={(e) => {
                      e.preventDefault()
                      e.stopPropagation()
                      indexRepo(r)
                    }}
                  >
                    Index
                  </button>
                </label>
              )
            })}

            {filtered.length === 0 && state.repos.length > 0 && (
              <div className="repo-empty">
                <p className="repo-empty-text">No repositories match “{query}”.</p>
              </div>
            )}

            {state.repos.length === 0 && (
              <div className="repo-empty">
                <p className="repo-empty-text">No repositories granted yet.</p>
                <button className="connect-btn" onClick={configure}>Add repositories</button>
              </div>
            )}
          </div>

          {state.repos.length > 0 && (
            <div className="repo-footer">
              <span className="repo-count">
                {selected.size > 0 ? `${selected.size} selected` : 'Select repositories to index'}
              </span>
              <button
                className="connect-btn repo-import-btn"
                disabled={selected.size === 0}
                onClick={importSelected}
              >
                Index {selected.size > 0 ? selected.size : ''} {selected.size === 1 ? 'repository' : 'repositories'}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}
