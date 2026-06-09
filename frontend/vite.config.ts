import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// Build output lands in the backend so Go can serve it as a single binary.
// Dev proxy forwards API + WebSocket traffic to the Go backend on :3000.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../backend/frontend/dist',
    emptyOutDir: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:3000',
      '/ws': { target: 'ws://localhost:3000', ws: true },
    },
  },
})
