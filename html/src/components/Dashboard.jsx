import Sidebar from './Sidebar'
import LogCard from './LogCard'
import ResolutionCard from './ResolutionCard'
import RepoExplorer from './RepoExplorer'
import IncidentsView from './IncidentsView'
import PatternsView from './PatternsView'
import PullRequestsView from './PullRequestsView'
import { clearSessionCache } from '../useSession'
import useIncidents, { summarise } from '../useIncidents'

// The heading for each view the sidebar routes to.
const TITLES = {
  dashboard: 'Dashboard',
  graph:     'Code graph',
  incidents: 'Incidents',
  prs:       'Pull requests',
  patterns:  'Patterns',
}

// Badges renders the live pipeline counters, hiding any that are zero.
function Badges({ stats }) {
  return (
    <>
      {stats.resolving > 0 && (
        <span className="badge badge-amber">{stats.resolving} resolving</span>
      )}
      {stats.resolvedToday > 0 && (
        <span className="badge badge-green">{stats.resolvedToday} resolved today</span>
      )}
      {stats.patternHits > 0 && (
        <span className="badge badge-blue" title="Resolved from the pattern cache — zero model tokens">
          {stats.patternHits} from cache
        </span>
      )}
    </>
  )
}

export default function Dashboard({ session, activeView, onNavigate }) {
  const { incidents } = useIncidents()
  const stats = summarise(incidents)

  const disconnect = async () => {
    clearSessionCache()
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
          <Badges stats={stats} />
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
            <h1 className="main-title">{TITLES[activeView] ?? 'Dashboard'}</h1>
            <div className="main-badges">
              <Badges stats={stats} />
            </div>
          </div>

          {activeView === 'dashboard' && (
            <>
              <RepoExplorer />
              <LogCard />
              <ResolutionCard />
            </>
          )}

          {activeView === 'graph' && <RepoExplorer />}

          {activeView === 'incidents' && <IncidentsView session={session} />}

          {activeView === 'prs' && <PullRequestsView />}

          {activeView === 'patterns' && <PatternsView />}
        </main>
      </div>
    </div>
  )
}
