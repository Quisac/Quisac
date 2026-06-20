function showDashboard() {
  document.getElementById('landing').style.display = 'none';
  document.getElementById('dashboard').style.display = 'block';
  document.body.style.overflow = 'hidden';
  document.body.style.height = '100vh';
}

function showLanding() {
  document.getElementById('dashboard').style.display = 'none';
  document.getElementById('landing').style.display = 'block';
  document.body.style.overflow = '';
  document.body.style.height = '';
}

// Sidebar active state
document.querySelectorAll('.sidebar-item').forEach(item => {
  item.addEventListener('click', () => {
    document.querySelectorAll('.sidebar-item').forEach(i => i.classList.remove('active'));
    item.classList.add('active');
  });
});

// ── Session cookie helpers ─────────────────────────────────────────────────
// The Go server stores session as hex-encoded JSON in "quisac_gh".
// We read it client-side to get the org name on page refresh.
function getSessionOrg() {
  const match = document.cookie.split(';').find(c => c.trim().startsWith('quisac_gh='));
  if (!match) return '';
  try {
    const hex = match.split('=')[1].trim();
    // hex → bytes → string
    const str = hex.match(/.{1,2}/g).map(b => String.fromCharCode(parseInt(b, 16))).join('');
    const session = JSON.parse(str);
    return session?.installation?.account?.login || '';
  } catch {
    return '';
  }
}

// ── OAuth callback handling ────────────────────────────────────────────────
const params = new URLSearchParams(window.location.search);

if (params.get('connected') === '1') {
  // Fresh OAuth redirect — org name is in the URL
  const org = params.get('org') || '';
  showDashboard();
  applyConnectedState(org);
  history.replaceState({}, '', '/');
} else if (getSessionOrg()) {
  // Already connected from a previous session — read org from cookie
  showDashboard();
  applyConnectedState(getSessionOrg());
}

function applyConnectedState(org) {
  // 1. Topbar: replace "Connect repo" with org pill
  const connectBtn = document.getElementById('connect-btn');
  if (connectBtn) connectBtn.style.display = 'none';

  const orgPill = document.getElementById('org-pill');
  const orgNameEl = document.getElementById('org-name');
  if (orgPill && orgNameEl) {
    orgNameEl.textContent = org || 'connected';
    orgPill.style.display = 'flex';
  }

  // 2. Sidebar: show org block above nav items
  const sidebarOrg = document.getElementById('sidebar-org');
  const sidebarOrgName = document.getElementById('sidebar-org-name');
  if (sidebarOrg && sidebarOrgName) {
    sidebarOrgName.textContent = org || 'connected';
    sidebarOrg.style.display = 'block';
  }

  // 3. Banner below dashboard header
  const banner = document.getElementById('repo-banner');
  const bannerText = document.getElementById('banner-text');
  if (banner && bannerText) {
    bannerText.textContent = org
      ? `${org} connected — Quisac is indexing your codebase`
      : 'GitHub connected — Quisac is indexing your codebase';
    banner.style.display = 'flex';
  }

  // 4. Graph card meta
  const graphMeta = document.getElementById('graph-meta');
  if (graphMeta && org) {
    graphMeta.textContent = `${org} · indexing…`;
  }
}
