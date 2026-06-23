const LOGS = [
  { time: '19:20:08', dot: 'ld-red',   msg: 'ConnectionTimeout — db-primary:5432 unreachable after 3 retries', svc: 'payment-svc' },
  { time: '19:20:09', dot: 'ld-red',   msg: 'High error rate — 5xx responses exceeding threshold (42%)',        svc: 'payment-svc' },
  { time: '19:20:11', dot: 'ld-red',   msg: 'Connection pool exhausted — max_connections=20 reached',           svc: 'db-pool'     },
  { time: '19:20:14', dot: 'ld-blue',  msg: 'Quisac — graph traversal started · path: payment-svc → db-pool → postgres', svc: 'quisac' },
  { time: '19:20:15', dot: 'ld-green', msg: 'Pattern matched — ConnectionPool misconfiguration · confidence 94% · 0 LLM tokens', svc: 'quisac' },
]

export default function LogCard() {
  return (
    <div className="log-card">
      <div className="card-header">
        <span className="card-title">Incident log</span>
        <span className="card-meta live">live · updated just now</span>
      </div>
      {LOGS.map((e, i) => (
        <div className="log-row" key={i}>
          <span className="log-time">{e.time}</span>
          <span className="log-msg">
            <span className={`log-dot ${e.dot}`} />
            {e.msg}
          </span>
          <span className="log-svc">{e.svc}</span>
        </div>
      ))}
    </div>
  )
}
