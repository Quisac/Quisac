const STEPS = [
  { num: '01', name: 'Alert ingestion',  desc: 'PagerDuty or Datadog fires a webhook. Quisac receives it in milliseconds and maps the failing service to a node in your live dependency graph — before anyone on your team sees the alert.', tag: 'webhook', cls: 'tag-blue' },
  { num: '02', name: 'Graph traversal',  desc: 'BFS from the failing node outward. Every candidate file is ranked by blast radius, fault history, and recency. Zero LLM tokens spent. Zero guesswork.', tag: '0 tokens', cls: 'tag-blue' },
  { num: '03', name: 'Pattern match',    desc: 'Seen this before? A cached fix is applied instantly. 65% of all incidents are resolved at this tier — no embedding lookup, no LLM call, no cost.', tag: '~65% of incidents', cls: 'tag-green' },
  { num: '04', name: 'LLM precision',    desc: 'For novel incidents, 2–3 ranked files go to a single precise prompt. The model sees only what matters — never your full codebase.', tag: '~18% of incidents', cls: 'tag-amber' },
  { num: '05', name: 'GitHub PR',        desc: "Fix validated and pushed as a ready-to-merge pull request. Your team reviews a solution — they don't debug from scratch at 3am.", tag: 'ready to merge', cls: 'tag-green' },
]

const FEATURES = [
  { icon: '⬡', name: 'Dependency graph engine',  desc: 'Static analysis of your entire codebase. Every service, every import, every call site — mapped and weighted by fault history. Built once, updated on every push.' },
  { icon: '⤳', name: 'Blast radius scoring',      desc: 'BFS traversal from the failing node ranks files by how likely they are to contain the root cause. No hallucinations. No random sampling.' },
  { icon: '◎', name: 'Three-tier resolution',     desc: 'Pattern match first. Embedding similarity second. LLM last resort — on 2–3 files only. 65% of incidents never touch the LLM. Your bill reflects that.' },
  { icon: '⤷', name: 'Ready-to-merge PRs',        desc: 'Fix validated, branch created, PR opened against your main branch. Commit message, description, affected files — all included. Your engineers merge, not triage.' },
]

function HeroGraph() {
  return (
    <svg className="hero-svg-bg" viewBox="0 0 1200 520" xmlns="http://www.w3.org/2000/svg" preserveAspectRatio="xMidYMid slice">
      <defs>
        <radialGradient id="bg1" cx="55%" cy="45%" r="60%">
          <stop offset="0%" stopColor="#d8e4f8" stopOpacity="1" />
          <stop offset="100%" stopColor="#eef2fb" stopOpacity="1" />
        </radialGradient>
        <radialGradient id="bg2" cx="80%" cy="55%" r="45%">
          <stop offset="0%" stopColor="#ddeee6" stopOpacity="0.55" />
          <stop offset="100%" stopColor="#eef2fb" stopOpacity="0" />
        </radialGradient>
        <filter id="soft"><feGaussianBlur stdDeviation="1.5" /></filter>
      </defs>

      <rect width="1200" height="520" fill="url(#bg1)" />
      <rect width="1200" height="520" fill="url(#bg2)" />

      {/* base edges — faint, spread across full width */}
      <g opacity="0.18" stroke="#7a96d4" strokeWidth="1" fill="none">
        <line x1="30"  y1="260" x2="120" y2="180" /><line x1="30"  y1="260" x2="120" y2="260" /><line x1="30"  y1="260" x2="120" y2="360" />
        <line x1="148" y1="180" x2="250" y2="130" /><line x1="148" y1="180" x2="250" y2="200" /><line x1="148" y1="260" x2="250" y2="290" /><line x1="148" y1="360" x2="250" y2="400" />
        <line x1="278" y1="130" x2="370" y2="100" /><line x1="278" y1="200" x2="370" y2="165" /><line x1="278" y1="200" x2="370" y2="230" /><line x1="278" y1="290" x2="370" y2="320" /><line x1="278" y1="400" x2="370" y2="430" />
        <line x1="398" y1="100" x2="490" y2="80"  /><line x1="398" y1="165" x2="490" y2="140" /><line x1="398" y1="230" x2="490" y2="260" /><line x1="398" y1="320" x2="490" y2="350" /><line x1="398" y1="430" x2="490" y2="450" />
        <line x1="518" y1="80"  x2="610" y2="110" /><line x1="518" y1="140" x2="610" y2="110" /><line x1="518" y1="260" x2="610" y2="280" /><line x1="518" y1="350" x2="610" y2="380" /><line x1="518" y1="450" x2="610" y2="430" />
        <line x1="638" y1="110" x2="730" y2="150" /><line x1="638" y1="280" x2="730" y2="260" /><line x1="638" y1="380" x2="730" y2="360" />
        <line x1="758" y1="150" x2="850" y2="130" /><line x1="758" y1="150" x2="850" y2="180" /><line x1="758" y1="260" x2="850" y2="280" />
        <line x1="878" y1="130" x2="960" y2="155" /><line x1="878" y1="180" x2="960" y2="155" /><line x1="878" y1="280" x2="960" y2="300" />
        <line x1="988" y1="155" x2="1080" y2="170" /><line x1="988" y1="300" x2="1080" y2="320" />
        <line x1="1108" y1="170" x2="1180" y2="160" /><line x1="1108" y1="320" x2="1180" y2="310" />
      </g>

      {/* traversal path — blue dashed left side, green right side */}
      <g fill="none">
        <line x1="30"  y1="260" x2="120" y2="260" stroke="#4361ee" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.4" />
        <line x1="148" y1="260" x2="250" y2="200" stroke="#4361ee" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.4" />
        <line x1="278" y1="200" x2="370" y2="165" stroke="#4361ee" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.4" />
        <line x1="398" y1="165" x2="490" y2="140" stroke="#4361ee" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.38" />
        <line x1="518" y1="140" x2="610" y2="110" stroke="#188038" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.42" />
        <line x1="638" y1="110" x2="730" y2="150" stroke="#188038" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.38" />
        <line x1="758" y1="150" x2="850" y2="130" stroke="#188038" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.33" />
        <line x1="878" y1="130" x2="960" y2="155" stroke="#188038" strokeWidth="1.5" strokeDasharray="5 4" opacity="0.28" />
      </g>

      {/* base nodes */}
      <g filter="url(#soft)">
        <circle cx="134" cy="180" r="10" fill="rgba(255,255,255,0.65)" stroke="rgba(255,255,255,0.92)" strokeWidth="1.5" />
        <circle cx="134" cy="360" r="10" fill="rgba(255,255,255,0.65)" stroke="rgba(255,255,255,0.92)" strokeWidth="1.5" />
        <circle cx="264" cy="130" r="9"  fill="rgba(255,255,255,0.62)" stroke="rgba(255,255,255,0.9)"  strokeWidth="1.5" />
        <circle cx="264" cy="290" r="9"  fill="rgba(255,255,255,0.62)" stroke="rgba(255,255,255,0.9)"  strokeWidth="1.5" />
        <circle cx="264" cy="400" r="9"  fill="rgba(255,255,255,0.62)" stroke="rgba(255,255,255,0.9)"  strokeWidth="1.5" />
        <circle cx="384" cy="100" r="9"  fill="rgba(255,255,255,0.6)"  stroke="rgba(255,255,255,0.88)" strokeWidth="1.5" />
        <circle cx="384" cy="230" r="9"  fill="rgba(255,255,255,0.6)"  stroke="rgba(255,255,255,0.88)" strokeWidth="1.5" />
        <circle cx="384" cy="320" r="9"  fill="rgba(255,255,255,0.6)"  stroke="rgba(255,255,255,0.88)" strokeWidth="1.5" />
        <circle cx="384" cy="430" r="8"  fill="rgba(255,255,255,0.6)"  stroke="rgba(255,255,255,0.88)" strokeWidth="1.5" />
        <circle cx="504" cy="80"  r="8"  fill="rgba(255,255,255,0.58)" stroke="rgba(255,255,255,0.85)" strokeWidth="1.5" />
        <circle cx="504" cy="260" r="8"  fill="rgba(255,255,255,0.58)" stroke="rgba(255,255,255,0.85)" strokeWidth="1.5" />
        <circle cx="504" cy="350" r="8"  fill="rgba(255,255,255,0.58)" stroke="rgba(255,255,255,0.85)" strokeWidth="1.5" />
        <circle cx="624" cy="280" r="8"  fill="rgba(255,255,255,0.56)" stroke="rgba(255,255,255,0.82)" strokeWidth="1.5" />
        <circle cx="624" cy="380" r="8"  fill="rgba(255,255,255,0.56)" stroke="rgba(255,255,255,0.82)" strokeWidth="1.5" />
        <circle cx="744" cy="260" r="8"  fill="rgba(255,255,255,0.54)" stroke="rgba(255,255,255,0.8)"  strokeWidth="1.5" />
        <circle cx="744" cy="360" r="7"  fill="rgba(255,255,255,0.54)" stroke="rgba(255,255,255,0.8)"  strokeWidth="1.5" />
        <circle cx="864" cy="180" r="7"  fill="rgba(255,255,255,0.52)" stroke="rgba(255,255,255,0.78)" strokeWidth="1.5" />
        <circle cx="864" cy="280" r="7"  fill="rgba(255,255,255,0.52)" stroke="rgba(255,255,255,0.78)" strokeWidth="1.5" />
        <circle cx="974" cy="155" r="6"  fill="rgba(255,255,255,0.5)"  stroke="rgba(255,255,255,0.75)" strokeWidth="1.5" />
        <circle cx="974" cy="300" r="6"  fill="rgba(255,255,255,0.5)"  stroke="rgba(255,255,255,0.75)" strokeWidth="1.5" />
        <circle cx="1094" cy="170" r="6" fill="rgba(255,255,255,0.48)" stroke="rgba(255,255,255,0.72)" strokeWidth="1.5" />
        <circle cx="1094" cy="320" r="6" fill="rgba(255,255,255,0.48)" stroke="rgba(255,255,255,0.72)" strokeWidth="1.5" />
        <circle cx="1180" cy="160" r="5" fill="rgba(255,255,255,0.45)" stroke="rgba(255,255,255,0.7)"  strokeWidth="1.5" />
        <circle cx="1180" cy="310" r="5" fill="rgba(255,255,255,0.45)" stroke="rgba(255,255,255,0.7)"  strokeWidth="1.5" />
      </g>

      {/* alert source */}
      <circle cx="30"  cy="260" r="14" fill="rgba(217,48,37,0.07)"  stroke="rgba(217,48,37,0.2)"   strokeWidth="1.5" />
      <circle cx="30"  cy="260" r="6"  fill="#d93025" opacity="0.72" />

      {/* traversal path nodes — blue */}
      <circle cx="134" cy="260" r="11" fill="rgba(255,255,255,0.88)" stroke="rgba(67,97,238,0.28)"  strokeWidth="1.5" />
      <circle cx="134" cy="260" r="4"  fill="#4361ee" opacity="0.52" />
      <circle cx="264" cy="200" r="11" fill="rgba(255,255,255,0.88)" stroke="rgba(67,97,238,0.28)"  strokeWidth="1.5" />
      <circle cx="264" cy="200" r="4"  fill="#4361ee" opacity="0.52" />
      <circle cx="384" cy="165" r="11" fill="rgba(255,255,255,0.88)" stroke="rgba(67,97,238,0.28)"  strokeWidth="1.5" />
      <circle cx="384" cy="165" r="4"  fill="#4361ee" opacity="0.52" />
      <circle cx="504" cy="140" r="11" fill="rgba(255,255,255,0.88)" stroke="rgba(67,97,238,0.25)"  strokeWidth="1.5" />
      <circle cx="504" cy="140" r="4"  fill="#4361ee" opacity="0.48" />

      {/* root cause — green */}
      <circle cx="624" cy="110" r="16" fill="rgba(24,128,56,0.08)"   stroke="rgba(24,128,56,0.26)"  strokeWidth="1.5" />
      <circle cx="624" cy="110" r="7"  fill="#188038" opacity="0.68" />

      {/* resolved nodes */}
      <circle cx="744" cy="150" r="12" fill="rgba(24,128,56,0.07)"   stroke="rgba(24,128,56,0.2)"   strokeWidth="1.5" />
      <circle cx="744" cy="150" r="5"  fill="#188038" opacity="0.58" />
      <circle cx="864" cy="130" r="10" fill="rgba(24,128,56,0.06)"   stroke="rgba(24,128,56,0.16)"  strokeWidth="1.5" />
      <circle cx="864" cy="130" r="4"  fill="#188038" opacity="0.5"  />
      <circle cx="974" cy="155" r="8"  fill="rgba(24,128,56,0.05)"   stroke="rgba(24,128,56,0.13)"  strokeWidth="1.5" />
      <circle cx="974" cy="155" r="3"  fill="#188038" opacity="0.4"  />

      {/* PR merged pill */}
      <rect x="1018" y="140" width="80" height="26" rx="13" fill="rgba(255,255,255,0.78)" stroke="rgba(24,128,56,0.38)" strokeWidth="1.5" />
      <text x="1058" y="157" textAnchor="middle" fontFamily="system-ui,sans-serif" fontSize="9.5" fill="#188038" fontWeight="500">PR merged</text>
      <line x1="974" y1="148" x2="1018" y2="150" stroke="rgba(24,128,56,0.22)" strokeWidth="1" strokeDasharray="3 3" />
    </svg>
  )
}

export default function Landing({ onConnect }) {
  const go = (e) => { e.preventDefault(); onConnect() }

  return (
    <div>

      {/* NAV */}
      <nav className="l-nav">
        <a className="l-nav-logo" href="#">Quis<span>ac</span></a>
        <div className="l-nav-links">
          <a className="nav-link" href="#how">How it works</a>
          <a className="nav-link" href="#features">Features</a>
          <a className="nav-signin" href="#" onClick={go}>Sign in</a>
          <a className="nav-cta" href="#" onClick={go}>Get early access</a>
        </div>
      </nav>

      {/* HERO */}
      <div className="hero-outer">
        <HeroGraph />
        <div className="hero-glass-wrap">
          <div className="hero-glass-card">
            <div className="hero-eyebrow">YC S2026</div>
            <h1 className="hero-title">
              Incidents fix themselves.<br />Your team just merges.
            </h1>
            <p className="hero-sub">
              The moment an alert fires, Quisac traverses your dependency graph,
              finds the root cause, and opens a ready-to-merge PR —
              before anyone on your team wakes up.
            </p>
            <div className="hero-cta-row">
              <a className="btn-primary" href="#" onClick={go}>Connect your repo</a>
              <a className="btn-secondary" href="#how">See how it works</a>
            </div>
          </div>
        </div>
      </div>

      {/* STATS STRIP */}
      <div className="stats-strip">
        <div className="stat-block">
          <div className="stat-val">65%</div>
          <div className="stat-lbl">resolved with zero LLM tokens</div>
        </div>
        <div className="stat-block">
          <div className="stat-val">&lt;90s</div>
          <div className="stat-lbl">alert to ready-to-merge PR</div>
        </div>
        <div className="stat-block">
          <div className="stat-val">2–3</div>
          <div className="stat-lbl">files ever seen by the LLM</div>
        </div>


      {/* LOGO STRIP */}
      

      {/* FEATURES */}
      <section className="section" id="features">
        <div className="section-label">WHY IT WORKS</div>
        <h2 className="section-title">Built on your dependency graph, not guesswork</h2>
        <p style={{ fontSize: '15px', color: 'var(--sub)', lineHeight: 1.7, maxWidth: '600px', marginBottom: '0.5rem' }}>
          Every other tool throws your codebase at an LLM and hopes for the best.
          Quisac builds a live graph of your services, traverses it from the failing node,
          and ranks candidate files before a single token is spent.
          The LLM is a last resort — not the first call.
        </p>
        <div className="feat-grid">
          {FEATURES.map((f) => (
            <div className="feat-card" key={f.name}>
              <div className="feat-icon">{f.icon}</div>
              <div className="feat-name">{f.name}</div>
              <div className="feat-desc">{f.desc}</div>
            </div>
          ))}
        </div>
      </section>



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
                <div className="prev-log-row">
                  <span style={{width:6,height:6,borderRadius:'50%',background:'#d93025',display:'inline-block',flexShrink:0}} />
                  ConnectionTimeout — db-primary unreachable
                  <span style={{ marginLeft: 'auto', color: '#80868b' }}>payment-svc</span>
                </div>
                <div className="prev-log-row">
                  <span style={{width:6,height:6,borderRadius:'50%',background:'#1a73e8',display:'inline-block',flexShrink:0}} />
                  Quisac — graph traversal started
                  <span style={{ marginLeft: 'auto', color: '#80868b' }}>quisac</span>
                </div>
                <div className="prev-log-row">
                  <span style={{width:6,height:6,borderRadius:'50%',background:'#188038',display:'inline-block',flexShrink:0}} />
                  Pattern matched — 0 LLM tokens · PR #247 open
                  <span style={{ marginLeft: 'auto', color: '#80868b' }}>quisac</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </section>



      {/* FINAL CTA */}
      <div className="final-cta">
        <div className="final-cta-inner">
          <h2 className="final-cta-title">Your next incident is already waiting.</h2>
          <p className="final-cta-sub">
            Connect your repo in 2 minutes. No credit card required.<br />
            First 10 resolutions are free.
          </p>
          <div style={{ display: 'flex', gap: '10px', justifyContent: 'center' }}>
            <a className="btn-primary" href="#" onClick={go}>Connect your repo →</a>
            <a className="btn-secondary" href="mailto:abhi@quisac.com">Talk to the founder</a>
          </div>
        </div>
      </div>

      {/* FOOTER */}
      <footer className="l-footer">
        <span className="footer-logo">Quisac</span>
        <div style={{ display: 'flex', gap: '1.5rem' }}>
          <a href="/privacy" className="nav-link" style={{ fontSize: '12px' }}>Privacy Policy</a>
          <a href="/terms"   className="nav-link" style={{ fontSize: '12px' }}>Terms of Service</a>
        </div>
        <span style={{ fontSize: '12px', color: 'var(--muted)' }}>© 2026 Quisac</span>
      </footer>

    </div>
  )
}
