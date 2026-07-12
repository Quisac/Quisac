import { useEffect, useState } from 'react'

// Module-level cache: /api/me is fetched exactly once per page load.
// React StrictMode unmounts and remounts components in dev, which was
// causing two back-to-back DB lookups per load (visible in logs as paired
// /api/me 200s). Since the session is immutable within a page visit, a
// single shared promise is safe.
let sessionCache = undefined      // undefined = not yet fetched
let sessionPromise = null

function fetchSession() {
  if (sessionCache !== undefined) return Promise.resolve(sessionCache)
  if (sessionPromise) return sessionPromise
  sessionPromise = fetch('/api/me', { credentials: 'same-origin' })
    .then((res) => (res.ok ? res.json() : null))
    .then((session) => { sessionCache = session; return session })
    .catch(() => { sessionCache = null; return null })
    .finally(() => { sessionPromise = null })
  return sessionPromise
}

// Call this after logout so the next connect re-fetches.
export function clearSessionCache() { sessionCache = undefined }

export default function useSession() {
  const [state, setState] = useState({ loading: true, session: null })

  useEffect(() => {
    let cancelled = false
    fetchSession()
      .then((session) => { if (!cancelled) setState({ loading: false, session }) })
      .catch(() => { if (!cancelled) setState({ loading: false, session: null }) })
    return () => { cancelled = true }
  }, [])

  return state
}
