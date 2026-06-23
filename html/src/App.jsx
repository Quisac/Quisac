import { useState } from 'react'
import Landing from './components/Landing'
import Dashboard from './components/Dashboard'
import useSession from './useSession'

export default function App() {
  // The session (from the HttpOnly cookie, via /api/me) decides what to show:
  // connected → dashboard, otherwise the landing page. No client-side auth
  // state to keep in sync.
  const { loading, session } = useSession()
  const [activeView, setActiveView] = useState('dashboard')

  if (loading) return null

  if (session?.connected) {
    return (
      <Dashboard
        session={session}
        activeView={activeView}
        onNavigate={setActiveView}
      />
    )
  }

  return <Landing />
}
