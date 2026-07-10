import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Proxy API calls and auth flows to the Go backend during `npm run dev`.
  // ALL routes that touch the Go server must be listed here so the session
  // cookie is always set from the same origin (localhost:5173). Missing a
  // route (e.g. /github/callback) means GitHub redirects to Vite's port but
  // the cookie gets set by Go on :8000 — a different origin — so every
  // subsequent /api/me call arrives with no cookie and returns 401.
  server: {
    proxy: {
      '/api':            'http://localhost:8000',
      '/github':         'http://localhost:8000',  // OAuth callback
      '/webhook':        'http://localhost:8000',  // GitHub push webhook
      '/healthz':        'http://localhost:8000',
      '/readyz':         'http://localhost:8000',
    },
  },
})

