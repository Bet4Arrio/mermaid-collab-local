import React from 'react'
import ReactDOM from 'react-dom/client'
import mermaid from 'mermaid'
import App from './App'
import './index.css'

// Initialize Mermaid once at startup; Preview.tsx renders imperatively.
mermaid.initialize({ startOnLoad: false, theme: 'default', securityLevel: 'loose' })

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
)
