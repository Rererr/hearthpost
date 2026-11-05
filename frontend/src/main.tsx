import React from 'react'
import { createRoot } from 'react-dom/client'
import './style.css'
import App from './App'
import { wailsBackend } from './wails'

createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App backend={wailsBackend} />
  </React.StrictMode>,
)
