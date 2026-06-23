export default function ResolutionCard() {
  return (
    <div className="res-card">
      <div className="res-header">
        <span className="res-title">
          <span className="res-check">✓</span>
          Resolution — incident #INC-4821
        </span>
        <span className="badge badge-green">resolved · pattern match</span>
      </div>
      <div className="res-body">
        <div className="res-details">
          <div>
            <div className="res-group-label">Root cause</div>
            <div className="res-group-val">db-pool misconfiguration</div>
            <div className="res-group-sub">max_connections too low for traffic spike</div>
          </div>
          <div>
            <div className="res-group-label">Resolution method</div>
            <div className="res-group-val">Pattern match — Tier 1</div>
            <div className="res-group-sub">0 LLM tokens used · resolved in 1.2s</div>
          </div>
        </div>

        <div className="files-label">Files identified by graph traversal</div>
        <div className="file-row">
          <span className="file-name">src/db/pool.py</span>
          <span><span className="diff diff-plus">+2</span> <span className="diff diff-minus">−1</span></span>
        </div>
        <div className="file-row">
          <span className="file-name">src/config/database.py</span>
          <span><span className="diff diff-plus">+1</span></span>
        </div>

        <div className="pr-row">
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '4px' }}>
              <span className="pr-commit">fix: increase db connection pool limit for payment-svc</span>
              <span className="badge-ready">ready to merge</span>
            </div>
            <div className="pr-sub">opened by quisac-bot · branch fix/inc-4821-db-pool · 2 files changed</div>
          </div>
          <button className="pr-btn">↑↓ View PR</button>
        </div>
      </div>
    </div>
  )
}
