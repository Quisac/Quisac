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

// sidebar active state
document.querySelectorAll('.sidebar-item').forEach(item => {
  item.addEventListener('click', () => {
    document.querySelectorAll('.sidebar-item').forEach(i => i.classList.remove('active'));
    item.classList.add('active');
  });
});