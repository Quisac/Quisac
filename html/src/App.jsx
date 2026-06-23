import { useState } from 'react'
import Landing from './components/Landing'
import Dashboard from './components/Dashboard'

export default function App() {
  // GitHub redirects back to /?connected=1 after install + authorize; land the
  // user straight on the dashboard in that case.
  const startConnected = new URLSearchParams(window.location.search).has('connected')
  const [view, setView] = useState(startConnected ? 'dashboard' : 'landing')
  const [activeView, setActiveView] = useState('dashboard')

  if (view === 'dashboard') {
    return (
      <Dashboard
        activeView={activeView}
        onNavigate={setActiveView}
        onShowLanding={() => setView('landing')}
      />
    )
  }

  return <Landing />
}
