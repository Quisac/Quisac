const NAV = [
  {
    section: 'MONITOR',
    items: [
      { id: 'dashboard', label: 'Dashboard' },
      { id: 'graph',     label: 'Code graph' },
      { id: 'incidents', label: 'Incidents'  },
    ],
  },
  {
    section: 'MANAGE',
    items: [
      { id: 'prs',      label: 'Pull requests' },
      { id: 'patterns', label: 'Patterns'      },
    ],
  },
]

export default function Sidebar({ active, onNavigate }) {
  return (
    <nav className="sidebar">
      {NAV.map(({ section, items }) => (
        <div className="sidebar-section" key={section}>
          <span className="sidebar-label">{section}</span>
          {items.map(({ id, label }) => (
            <div
              key={id}
              className={`sidebar-item${active === id ? ' active' : ''}`}
              onClick={() => onNavigate(id)}
            >
              {label}
            </div>
          ))}
        </div>
      ))}
    </nav>
  )
}
