import { useEffect, useState, useCallback, useRef } from 'react'

// Shared incident feed. A resolution can move from queued to an open pull
// request in under a minute, so this polls — but only while something is in
// flight, and a quiet dashboard makes no requests at all.

const POLL_MS = 5000

export const isActive = (inc) =>
  inc?.resolution?.status === 'pending' ||
  inc?.resolution?.status === 'resolving' ||
  inc?.status === 'traversing'

export default function useIncidents() {
  const [state, setState] = useState({ loading: true, incidents: [], error: null })
  const timer = useRef(null)

  const load = useCallback(async () => {
    try {
      const res = await fetch('/api/incidents', { credentials: 'same-origin' })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      setState({ loading: false, incidents: (await res.json()) ?? [], error: null })
    } catch (e) {
      setState((s) => ({ ...s, loading: false, error: String(e.message ?? e) }))
    }
  }, [])

  useEffect(() => { load() }, [load])

  // Dropped locally rather than awaiting a refetch, so the feed settles at
  // once. A 404 means someone else already closed it — the same outcome.
  const remove = useCallback(async (id) => {
    const res = await fetch(`/api/incidents/${encodeURIComponent(id)}`, {
      method: 'DELETE',
      credentials: 'same-origin',
    })
    if (!res.ok && res.status !== 404) {
      throw new Error((await res.text())?.trim() || `HTTP ${res.status}`)
    }
    setState((s) => ({ ...s, incidents: s.incidents.filter((i) => i.id !== id) }))
  }, [])

  useEffect(() => {
    clearTimeout(timer.current)
    if (state.incidents.some(isActive)) timer.current = setTimeout(load, POLL_MS)
    return () => clearTimeout(timer.current)
  }, [state.incidents, load])

  return { ...state, reload: load, remove }
}

// summarise turns the feed into the counters the dashboard header shows.
export function summarise(incidents) {
  const dayAgo = Date.now() - 24 * 60 * 60 * 1000
  let resolving = 0
  let resolvedToday = 0
  let tokensSaved = 0

  for (const inc of incidents) {
    if (isActive(inc)) resolving++
    const res = inc.resolution
    if (res?.status === 'resolved' && res.resolved_at && new Date(res.resolved_at).getTime() >= dayAgo) {
      resolvedToday++
    }
    // Pattern-cache hits are the incidents resolved without the model.
    if (res?.status === 'resolved' && res.method === 'pattern') tokensSaved++
  }
  return { resolving, resolvedToday, patternHits: tokensSaved, total: incidents.length }
}

// useFaultGraph loads the subgraph connecting an incident's ranked candidates
// back to its entry point. It is not part of the polled feed: the Neo4j query is
// only worth running while someone is looking, so it fetches once on mount —
// expanding the evidence panel is what mounts it.
export function useFaultGraph(incidentId) {
  const [state, setState] = useState({ id: incidentId, loading: true, graph: null, error: null })

  useEffect(() => {
    if (!incidentId) return
    let cancelled = false

    ;(async () => {
      try {
        const res = await fetch(`/api/incidents/${encodeURIComponent(incidentId)}/graph`, {
          credentials: 'same-origin',
        })
        if (!res.ok) throw new Error((await res.text())?.trim() || `HTTP ${res.status}`)
        const graph = await res.json()
        if (!cancelled) setState({ id: incidentId, loading: false, graph, error: null })
      } catch (e) {
        if (!cancelled) setState({ id: incidentId, loading: false, graph: null, error: String(e.message ?? e) })
      }
    })()

    return () => { cancelled = true }
  }, [incidentId])

  // Derived during render rather than reset from the effect, so a changed id
  // never shows the previous incident's graph for a frame.
  if (state.id !== incidentId) return { loading: true, graph: null, error: null }
  return state
}
