import Sidebar from './Sidebar'
import GraphCard from './GraphCard'
import LogCard from './LogCard'
import ResolutionCard from './ResolutionCard'
import RepoPicker from './RepoPicker'

export default function Dashboard({ session, activeView, onNavigate }) {
  const disconnect = async () => {
    await fetch('/api/logout', { method: 'POST', credentials: 'same-origin' })
    window.location.href = '/'
  }

  return (
    <div className="dash-shell">
      <header className="dash-topbar">
        <div className="dash-topbar-left">
          <span className="dash-logo">
            Quis<span>ac</span>
          </span>
        </div>
        <div className="dash-topbar-right">
          <span className="badge badge-amber">1 resolving</span>
          <span className="badge badge-green">12 resolved today</span>
          <span className="dash-account">
            Connected as <strong>{session.login || 'GitHub'}</strong>
          </span>
          <button className="connect-btn" onClick={disconnect}>Disconnect</button>
        </div>
      </header>

      <div className="dash-body">
        <Sidebar active={activeView} onNavigate={onNavigate} />

        <main className="main">
          <div className="main-header">
            <h1 className="main-title">
              {activeView === 'dashboard' && 'Dashboard'}
              {activeView === 'graph'     && 'Code graph'}
              {activeView === 'incidents' && 'Incidents'}
              {activeView === 'prs'       && 'Pull requests'}
              {activeView === 'patterns'  && 'Patterns'}
            </h1>
            <div className="main-badges">
              <span className="badge badge-amber">1 resolving</span>
              <span className="badge badge-green">12 resolved today</span>
            </div>
          </div>

          {activeView === 'dashboard' && (
            <>
              <RepoPicker />
              <LogCard />
              <ResolutionCard />
            </>
          )}

          {activeView === 'graph' && (
            <GraphCard />
          )}

          {activeView !== 'dashboard' && activeView !== 'graph' && (
            <div className="coming-soon">
              This view is coming soon.
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
