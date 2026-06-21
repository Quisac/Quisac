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

// ── Session cookie helpers ────────────────────────────────────────────────────
function getSession() {
  const match = document.cookie.split(';').find(c => c.trim().startsWith('quisac_gh='));
  if (!match) return null;
  try {
    const hex = match.split('=')[1].trim();
    const str = hex.match(/.{1,2}/g).map(b => String.fromCharCode(parseInt(b, 16))).join('');
    return JSON.parse(str);
  } catch {
    return null;
  }
}

// ── OAuth callback handling ───────────────────────────────────────────────────
const params = new URLSearchParams(window.location.search);

if (params.get('connected') === '1') {
  const org  = params.get('org')  || '';
  const repo = params.get('repo') || '';
  showDashboard();
  applyConnectedState(org, repo);
  history.replaceState({}, '', '/');
} else {
  const sess = getSession();
  if (sess?.installation?.account?.login) {
    showDashboard();
    applyConnectedState(sess.installation.account.login, sess.selected_repo || '');
  }
}

function applyConnectedState(org, repo) {
  // 1. Topbar: swap "Connect repo" for org pill
  const connectBtn = document.getElementById('connect-btn');
  if (connectBtn) connectBtn.style.display = 'none';

  const orgPill   = document.getElementById('org-pill');
  const orgNameEl = document.getElementById('org-name');
  if (orgPill && orgNameEl) {
    orgNameEl.textContent = org || 'connected';
    orgPill.style.display = 'flex';
  }

  // 2. Sidebar org block
  const sidebarOrg     = document.getElementById('sidebar-org');
  const sidebarOrgName = document.getElementById('sidebar-org-name');
  if (sidebarOrg && sidebarOrgName) {
    sidebarOrgName.textContent = org || 'connected';
    sidebarOrg.style.display = 'block';
  }

  // 3. Banner
  const banner     = document.getElementById('repo-banner');
  const bannerText = document.getElementById('banner-text');
  if (banner && bannerText) {
    bannerText.textContent = repo
      ? `${repo} connected — Quisac is indexing your codebase`
      : org
        ? `${org} connected — Quisac is indexing your codebase`
        : 'GitHub connected — Quisac is indexing your codebase';
    banner.style.display = 'flex';
  }

  // 4. Graph card meta
  const graphMeta = document.getElementById('graph-meta');
  if (graphMeta) {
    graphMeta.textContent = repo
      ? `${repo} · indexing…`
      : org
        ? `${org} · indexing…`
        : graphMeta.textContent;
  }
}
