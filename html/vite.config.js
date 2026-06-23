import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Proxy API calls to the Go backend during `npm run dev` so the connect
  // flow works without a production build. In prod the Go server serves both.
  server: {
    proxy: {
      '/api': 'http://localhost:8000',
    },
  },
})
