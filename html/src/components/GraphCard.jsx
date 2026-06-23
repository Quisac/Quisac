export default function GraphCard() {
  return (
    <div className="graph-card">
      <div className="card-header">
        <span className="card-title">Codebase graph</span>
        <span className="card-meta">47 services · 312 edges · indexed 4 min ago</span>
      </div>
      <div className="graph-body">
        <svg viewBox="0 0 760 180" style={{ width: '100%', height: '100%' }}>
          <line x1="100" y1="90" x2="190" y2="55"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="100" y1="90" x2="190" y2="90"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="100" y1="90" x2="190" y2="125" stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="270" y1="55"  x2="360" y2="40"  stroke="#fce8e6" strokeWidth="1.5"/>
          <line x1="270" y1="55"  x2="360" y2="68"  stroke="#fce8e6" strokeWidth="1.5"/>
          <line x1="270" y1="90"  x2="360" y2="90"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="270" y1="125" x2="360" y2="125" stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="440" y1="40"  x2="530" y2="55"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="440" y1="68"  x2="530" y2="55"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="440" y1="90"  x2="530" y2="90"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="530" y1="55"  x2="620" y2="72"  stroke="#e8eaed" strokeWidth="1.5"/>
          <line x1="530" y1="90"  x2="620" y2="72"  stroke="#e8eaed" strokeWidth="1.5"/>

          <rect x="30"  y="74" width="70" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="65"  y="92" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">api-gateway</text>

          <rect x="190" y="40" width="80" height="28" rx="5" fill="#fce8e6" stroke="#d93025" strokeWidth="1.5"/>
          <circle cx="202" cy="50" r="4" fill="#d93025"/>
          <text x="230" y="58" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#d93025" fontWeight="500">payment-svc</text>

          <rect x="190" y="76" width="80" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="230" y="94" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">auth-svc</text>

          <rect x="190" y="111" width="80" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="230" y="129" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">user-svc</text>

          <rect x="360" y="25" width="80" height="28" rx="5" fill="#fef0cd" stroke="#e37400" strokeWidth="1.5"/>
          <circle cx="372" cy="35" r="4" fill="#e37400"/>
          <text x="400" y="43" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#e37400" fontWeight="500">db-pool</text>

          <rect x="360" y="53" width="80" height="28" rx="5" fill="#fce8e6" stroke="#d93025" strokeWidth="1.5"/>
          <text x="400" y="71" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#d93025">postgres</text>

          <rect x="360" y="76" width="80" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="400" y="94" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">cache</text>

          <rect x="360" y="111" width="80" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="400" y="129" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">stripe-svc</text>

          <rect x="530" y="40" width="70" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="565" y="58" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">queue</text>

          <rect x="530" y="76" width="70" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="565" y="94" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">worker</text>

          <rect x="620" y="58" width="80" height="28" rx="5" fill="#fff" stroke="#dadce0" strokeWidth="1.5"/>
          <text x="660" y="76" textAnchor="middle" fontFamily="Google Sans,sans-serif" fontSize="8.5" fill="#5f6368">notif-svc</text>
        </svg>
      </div>
      <div className="graph-legend">
        <div className="leg-item"><span className="leg-dot leg-fail" />failing</div>
        <div className="leg-item"><span className="leg-dot leg-root" />root cause</div>
        <div className="leg-item"><span className="leg-dot leg-healthy" />healthy</div>
      </div>
    </div>
  )
}
