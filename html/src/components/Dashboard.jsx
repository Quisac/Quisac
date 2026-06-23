import Sidebar from './Sidebar'
import GraphCard from './GraphCard'
import LogCard from './LogCard'
import ResolutionCard from './ResolutionCard'

export default function Dashboard({ activeView, onNavigate, onShowLanding }) {
  return (
    <div className="dash-shell">
      <header className="dash-topbar">
        <div className="dash-topbar-left">
          <span className="dash-logo" onClick={onShowLanding}>
            Quis<span>ac</span>
          </span>
        </div>
        <div className="dash-topbar-right">
          <span className="badge badge-amber">1 resolving</span>
          <span className="badge badge-green">12 resolved today</span>
          <a className="connect-btn" href="#">Connect repo</a>
        </div>
      </header>

      <div className="dash-body">
        <Sidebar active={activeView} onNavigate={onNavigate} />

        <main className="main">
          <div className="main-header">
            <h1 className="main-title">Dashboard</h1>
            <div className="main-badges">
              <span className="badge badge-amber">1 resolving</span>
              <span className="badge badge-green">12 resolved today</span>
            </div>
          </div>

          {activeView === 'dashboard' && (
            <>
              <GraphCard />
              <LogCard />
              <ResolutionCard />
            </>
          )}

          {activeView !== 'dashboard' && (
            <div className="coming-soon">
              This view is coming soon.
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
