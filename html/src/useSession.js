import { useEffect, useState } from 'react'

// useSession asks the backend who we are. The HttpOnly session cookie is the
// source of truth for "am I connected?"; this hook surfaces it to the SPA so
// components don't keep their own (drift-prone) copy of auth state.
//
// Returns { loading, session } where session is { connected, login,
// installationId } when connected, or null otherwise.
export default function useSession() {
  const [state, setState] = useState({ loading: true, session: null })

  useEffect(() => {
    let cancelled = false
    fetch('/api/me', { credentials: 'same-origin' })
      .then((res) => (res.ok ? res.json() : null))
      .then((session) => { if (!cancelled) setState({ loading: false, session }) })
      .catch(() => { if (!cancelled) setState({ loading: false, session: null }) })
    return () => { cancelled = true }
  }, [])

  return state
}
