import React from 'react'
import { createRoot } from 'react-dom/client'
import App from './App'
import './style.css'

async function boot() {
  // In a plain browser during `npm run dev` there is no Wails runtime; use a
  // simulated backend so the UI can be developed and tested visually. The
  // mock is never included in production builds.
  if (import.meta.env.DEV && !window.go) {
    const { installMockBackend } = await import('./dev/mockBackend')
    installMockBackend()
  }
  createRoot(document.getElementById('root')!).render(
    <React.StrictMode>
      <App />
    </React.StrictMode>,
  )
}

void boot()
