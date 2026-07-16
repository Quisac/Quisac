const STEPS = [
  { num: '01', name: 'Alert ingestion',  desc: 'System down. Alert fires. While your team is still waking up, Quisac receives the webhook and maps the failing service to a node in your dependency graph — in milliseconds.', tag: 'webhook',            cls: 'tag-blue'  },
  { num: '02', name: 'Graph traversal',  desc: 'BFS from the failing node. Candidate files ranked by blast radius, fault history, and recency — zero LLM tokens.',                                                           tag: '0 tokens',          cls: 'tag-blue'  },
  { num: '03', name: 'Pattern match',    desc: 'Known incident? Cached fix applied instantly. 65% of incidents never reach the LLM.',                                                                                         tag: '~65% of incidents', cls: 'tag-green' },
  { num: '04', name: 'LLM fix',          desc: 'Last resort. 2–3 files, one precise prompt. The LLM sees only what matters — never the full repo.',                                                                          tag: '~18% of incidents', cls: 'tag-amber' },
  { num: '05', name: 'GitHub PR',        desc: 'Fix validated and pushed as a ready-to-merge PR. Your team reviews, not triages.',                                                                                            tag: 'ready to merge',    cls: 'tag-green' },
]

export default function Landing({ onConnect }) {
  // Every auth CTA opens the in-app provider-select step.
  const go = (e) => { e.preventDefault(); onConnect() }

  return (
    <div>
      {/* NAV */}
      <nav className="l-nav">
        <a className="l-nav-logo" href="#">Quis<span>ac</span></a>
        <div className="l-nav-links">
          <a className="nav-link" href="#how">How it works</a>
          <a className="nav-link" href="#preview">Dashboard</a>
          <a className="nav-signin" href="#" onClick={go}>Sign in</a>
          <a className="nav-cta" href="#" onClick={go}>Get early access</a>
        </div>
      </nav>

      {/* HERO */}
      <div className="hero">
        <div className="hero-eyebrow"></div>
        <h1 className="hero-title">
          Incidents fix themselves.<br />Your team just merges.
        </h1>
        <p className="hero-sub">
          The moment an alert fires, Quisac takes over — traverses your codebase,
          finds the root cause, and opens a ready-to-merge PR.
          No pages. No 3am standups. No triage.
        </p>
        <div className="hero-cta-row">
          <a className="btn-primary" href="#" onClick={go}>Connect your repo</a>
          <a className="btn-secondary" href="#how">See how it works</a>
        </div>
        <div className="hero-stats">
          <div className="stat">
            <div className="stat-val">65%</div>
            <div className="stat-lbl">resolved with zero LLM tokens</div>
          </div>
          <div className="stat-divider" />
          <div className="stat">
            <div className="stat-val">&lt;90s</div>
            <div className="stat-lbl">alert to ready-to-merge PR</div>
          </div>
          <div className="stat-divider" />
          <div className="stat">
            <div className="stat-val">2–3</div>
            <div className="stat-lbl">files ever seen by the LLM</div>
          </div>
        </div>
      </div>

      {/* HOW IT WORKS */}
      <section className="section" id="how">
        <div className="section-label">HOW IT WORKS</div>
        <h2 className="section-title">Alert to PR in under 90 seconds</h2>
        <div className="pipeline">
          {STEPS.map((s, i, arr) => (
            <>
              <div className="pipe-step" key={s.num}>
                <div className="pipe-num">{s.num}</div>
                <div className="pipe-name">{s.name}</div>
                <div className="pipe-desc">{s.desc}</div>
                <span className={`pipe-tag ${s.cls}`}>{s.tag}</span>
              </div>
              {i < arr.length - 1 && <div className="pipe-arrow" key={`a${i}`}>→</div>}
            </>
          ))}
        </div>
      </section>

      {/* DASHBOARD PREVIEW */}
      <section className="section" id="preview">
        <div className="section-label">DASHBOARD PREVIEW</div>
        <h2 className="section-title">Everything in one place</h2>
        <div className="preview-frame">
          <div className="preview-topbar">
            <span className="preview-logo">Quis<span>ac</span></span>
            <div className="preview-badges">
              <span className="badge badge-amber">1 resolving</span>
              <span className="badge badge-green">12 resolved today</span>
            </div>
          </div>
          <div className="preview-body">
            <div className="preview-sidebar">
              <div className="prev-item active">Dashboard</div>
              <div className="prev-item">Code graph</div>
              <div className="prev-item">Incidents</div>
              <div className="prev-item">Pull requests</div>
            </div>
            <div className="preview-main">
              <div className="prev-card">
                <div className="prev-card-header">
                  <span>Codebase graph</span>
                  <span style={{ fontSize: '11px', color: '#80868b' }}>47 services · 312 edges</span>
                </div>
                <svg viewBox="0 0 500 110" style={{ width: '100%', height: '110px' }}>
                  <line x1="80" y1="55" x2="160" y2="35"  stroke="#e8eaed" strokeWidth="1.5"/>
                  <line x1="80" y1="55" x2="160" y2="55"  stroke="#e8eaed" strokeWidth="1.5"/>
                  <line x1="80" y1="55" x2="160" y2="75"  stroke="#e8eaed" strokeWidth="1.5"/>
                  <line x1="230" y1="35" x2="310" y2="25" stroke="#fce8e6" strokeWidth="1.5"/>
                  <line x1="230" y1="35" x2="310" y2="48" stroke="#fce8e6" strokeWidth="1.5"/>
                  <line x1="230" y1="55" x2="310" y2="65" stroke="#e8eaed" strokeWidth="1.5"/>
                  <line x1="380" y1="35" x2="440" y2="50" stroke="#e8eaed" strokeWidth="1.5"/>
                  <line x1="380" y1="55" x2="440" y2="50" stroke="#e8eaed" strokeWidth="1.5"/>
                  <rect x="20"  y="41" width="60" height="24" rx="4" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
                  <text x="50"  y="57" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#5f6368">api-gateway</text>
                  <rect x="160" y="22" width="70" height="24" rx="4" fill="#fce8e6" stroke="#d93025" strokeWidth="1.5"/>
                  <text x="195" y="38" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#d93025" fontWeight="500">payment-svc</text>
                  <rect x="160" y="43" width="70" height="24" rx="4" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
                  <text x="195" y="59" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#5f6368">auth-svc</text>
                  <rect x="160" y="63" width="70" height="24" rx="4" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
                  <text x="195" y="79" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#5f6368">user-svc</text>
                  <rect x="310" y="12" width="70" height="24" rx="4" fill="#fef0cd" stroke="#e37400" strokeWidth="1.5"/>
                  <text x="345" y="28" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#e37400" fontWeight="500">db-pool</text>
                  <rect x="310" y="35" width="70" height="24" rx="4" fill="#fce8e6" stroke="#d93025" strokeWidth="1.5"/>
                  <text x="345" y="51" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#d93025">postgres</text>
                  <rect x="310" y="53" width="70" height="24" rx="4" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
                  <text x="345" y="69" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#5f6368">cache</text>
                  <rect x="440" y="38" width="50" height="24" rx="4" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
                  <text x="465" y="54" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="7.5" fill="#5f6368">queue</text>
                </svg>
              </div>
              <div className="prev-card">
                <div className="prev-card-header">
                  <span>Incident log</span>
                  <span style={{ fontSize: '11px', color: '#188038' }}>live</span>
                </div>
                <div className="prev-log-row"><span className="log-dot ld-red" style={{width:6,height:6,borderRadius:'50%',background:'#d93025',display:'inline-block',flexShrink:0}} />ConnectionTimeout — db-primary unreachable<span style={{ marginLeft: 'auto', color: '#80868b' }}>payment-svc</span></div>
                <div className="prev-log-row"><span style={{width:6,height:6,borderRadius:'50%',background:'#1a73e8',display:'inline-block',flexShrink:0}} />Quisac — graph traversal started<span style={{ marginLeft: 'auto', color: '#80868b' }}>quisac</span></div>
                <div className="prev-log-row"><span style={{width:6,height:6,borderRadius:'50%',background:'#188038',display:'inline-block',flexShrink:0}} />Pattern matched — 0 LLM tokens<span style={{ marginLeft: 'auto', color: '#80868b' }}>quisac</span></div>
              </div>
            </div>
          </div>
        </div>
        <div style={{ textAlign: 'center', marginTop: '2rem' }}>
          <a className="btn-primary" href="#" onClick={go}>Connect your repo →</a>
        </div>
      </section>

      {/* FOOTER */}
      <footer className="l-footer">
        <span className="footer-logo">Quisac</span>
        <a href="mailto:hello@quisac.com" className="nav-link">sentinel.team.co@gmail.com</a>
      </footer>
    </div>
  )
}
