import { useState } from 'react'
import Landing from './components/Landing'
import Dashboard from './components/Dashboard'

export default function App() {
  const [view, setView] = useState('landing')
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

  return <Landing onShowDashboard={() => setView('dashboard')} />
}
