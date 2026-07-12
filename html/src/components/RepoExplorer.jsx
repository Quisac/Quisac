import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import GraphCanvas from './GraphCanvas'

const goInstall = () => { window.location.href = '/api/install' }

const splitName = (full) => {
  const i = full.lastIndexOf('/')
  return i === -1 ? { owner: '', name: full } : { owner: full.slice(0, i), name: full.slice(i + 1) }
}

const timeAgo = (ts) => {
  const s = Math.round((Date.now() - ts) / 1000)
  if (s < 60) return 'just now'
  if (s < 3600) return `${Math.floor(s / 60)}m ago`
  return `${Math.floor(s / 3600)}h ago`
}

// Module-level cache: /api/repos is fetched once per page load, not on every
// component mount. React StrictMode unmounts and remounts components in dev,
// which was causing a fresh GitHub API call (taking ~800ms) on every remount.
let reposCache = null
let reposFetchPromise = null

function fetchRepos() {
  if (reposCache !== null) return Promise.resolve(reposCache)
  if (reposFetchPromise) return reposFetchPromise
  reposFetchPromise = fetch('/api/repos', { credentials: 'same-origin' })
    .then(async (res) => {
      if (res.status === 401) return { status: 'unauth' }
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const repos = await res.json()
      reposCache = { status: 'ready', repos: repos ?? [] }
      return reposCache
    })
    .finally(() => { reposFetchPromise = null })
  return reposFetchPromise
}

export default function RepoExplorer() {
  const [repoState, setRepoState] = useState({ status: 'loading', repos: [], error: null })
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState(null)   // full_name
  const [byRepo, setByRepo] = useState({})         // full_name -> { status, graph, error, nodes, edges, at }
  const pollRef = useRef(null)

  useEffect(() => {
    let cancelled = false
    fetchRepos()
      .then((d) => { if (!cancelled) setRepoState({ repos: [], error: null, ...d }) })
      .catch((e) => { if (!cancelled) setRepoState({ status: 'error', repos: [], error: e.message }) })
    return () => {
      cancelled = true
      // Only clear the polling interval on unmount — don't cancel the repos
      // fetch since that result is shared via the module cache.
      clearInterval(pollRef.current)
    }
  }, [])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return q ? repoState.repos.filter((r) => r.full_name.toLowerCase().includes(q)) : repoState.repos
  }, [repoState.repos, query])

  const patch = useCallback((repo, next) => {
    setByRepo((prev) => ({ ...prev, [repo]: { ...prev[repo], ...next } }))
  }, [])

  const index = useCallback((repo) => {
    clearInterval(pollRef.current)
    patch(repo, { status: 'pending', error: null })

    fetch('/api/index', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ repo }),
    })
      .then(async (res) => {
        if (!res.ok) throw new Error(await res.text())
        return res.json()
      })
      .then(({ jobId }) => {
        pollRef.current = setInterval(async () => {
          try {
            const res = await fetch(`/api/graph/${jobId}`, { credentials: 'same-origin' })
            const data = await res.json()
            if (data.status === 'done') {
              clearInterval(pollRef.current)
              const g = typeof data.graph === 'string' ? JSON.parse(data.graph) : data.graph
              patch(repo, {
                status: 'done',
                graph: g,
                nodes: Object.keys(g.nodes ?? {}).length,
                edges: (g.edges ?? []).length,
                at: Date.now(),
              })
            } else if (data.status === 'failed') {
              clearInterval(pollRef.current)
              patch(repo, { status: 'error', error: data.error || 'Indexing failed' })
            }
          } catch (e) {
            clearInterval(pollRef.current)
            patch(repo, { status: 'error', error: e.message })
          }
        }, 1500)
      })
      .catch((e) => patch(repo, { status: 'error', error: e.message }))
  }, [patch])

  const entry = selected ? byRepo[selected] : null
  const status = entry?.status ?? 'none'

  return (
    <div className="explorer">
      {/* ── Repository rail ── */}
      <aside className="ex-rail">
        <div className="ex-rail-head">
          <span className="ex-rail-title">Repositories</span>
          <button className="ex-manage" onClick={goInstall}>Manage</button>
        </div>

        <div className="ex-search-wrap">
          <svg className="ex-search-icon" width="14" height="14" viewBox="0 0 16 16" fill="none">
            <circle cx="7" cy="7" r="4.5" stroke="currentColor" strokeWidth="1.4" />
            <path d="m11 11 3 3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
          </svg>
          <input
            className="ex-search"
            placeholder="Search repositories"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>

        <div className="ex-list">
          {repoState.status === 'loading' &&
            [0, 1, 2, 3].map((i) => <div className="ex-row ex-row-skel" key={i}><span className="ex-skel" /></div>)}

          {repoState.status === 'unauth' && (
            <div className="ex-rail-empty">Connect GitHub to list repositories.</div>
          )}
          {repoState.status === 'error' && (
            <div className="ex-rail-empty">Couldn’t load repositories — {repoState.error}</div>
          )}

          {repoState.status === 'ready' && filtered.map((r) => {
            const { owner, name } = splitName(r.full_name)
            const st = byRepo[r.full_name]?.status
            const isSel = selected === r.full_name
            return (
              <button
                key={r.id}
                className={`ex-row${isSel ? ' is-active' : ''}`}
                onClick={() => setSelected(r.full_name)}
              >
                <span className={`ex-dot ex-dot-${st ?? 'idle'}`} />
                <span className="ex-row-name">
                  {owner && <span className="ex-row-owner">{owner}/</span>}{name}
                </span>
                {r.private && <span className="ex-row-lock" title="Private">•</span>}
              </button>
            )
          })}

          {repoState.status === 'ready' && filtered.length === 0 && (
            <div className="ex-rail-empty">No matches for “{query}”.</div>
          )}
        </div>
      </aside>

      {/* ── Canvas panel ── */}
      <section className="ex-panel">
        <header className="ex-toolbar">
          <div className="ex-toolbar-info">
            <span className="ex-repo-name">{selected || 'No repository selected'}</span>
            <span className="ex-repo-meta">
              {status === 'done'
                ? `${entry.nodes.toLocaleString()} nodes · ${entry.edges.toLocaleString()} edges · indexed ${timeAgo(entry.at)}`
                : status === 'pending' ? 'Building graph…'
                : selected ? 'Not indexed yet'
                : 'Pick a repository from the left'}
            </span>
          </div>
          {selected && (
            <button
              className={`ex-action${status === 'done' ? ' is-secondary' : ''}`}
              disabled={status === 'pending'}
              onClick={() => index(selected)}
            >
              {status === 'pending' ? 'Indexing…'
                : status === 'done' ? 'Re-index'
                : status === 'error' ? 'Retry'
                : 'Index repository'}
            </button>
          )}
        </header>

        <div className="ex-stage">
          {status === 'done' && <GraphCanvas graph={entry.graph} />}

          {status !== 'done' && (
            <div className="ex-stage-empty">
              {status === 'pending' && (
                <>
                  <div className="ex-spinner" />
                  <p className="ex-empty-title">Indexing {selected}</p>
                  <p className="ex-empty-sub">Parsing files and building the dependency graph.</p>
                </>
              )}
              {status === 'error' && (
                <>
                  <p className="ex-empty-title">Indexing failed</p>
                  <p className="ex-empty-sub">{entry.error}</p>
                </>
              )}
              {(status === 'idle' || status === 'none') && selected && (
                <>
                  <GraphGlyph />
                  <p className="ex-empty-title">{selected} isn’t indexed yet</p>
                  <p className="ex-empty-sub">Index it to explore its dependency graph.</p>
                  <button className="ex-action" onClick={() => index(selected)}>Index repository</button>
                </>
              )}
              {!selected && (
                <>
                  <GraphGlyph />
                  <p className="ex-empty-title">Select a repository</p>
                  <p className="ex-empty-sub">Choose a repo on the left to view or build its graph.</p>
                </>
              )}
            </div>
          )}
        </div>
      </section>
    </div>
  )
}

function GraphGlyph() {
  return (
    <svg className="ex-glyph" width="52" height="52" viewBox="0 0 52 52" fill="none" aria-hidden="true">
      <circle cx="26" cy="12" r="4" />
      <circle cx="12" cy="38" r="4" />
      <circle cx="40" cy="38" r="4" />
      <circle cx="26" cy="30" r="3" />
      <path d="M26 16v11M23.5 28.5 14 36M28.5 28.5 38 36" strokeWidth="1.6" />
    </svg>
  )
}
