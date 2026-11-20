import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App.jsx'
import { CrashBoundary } from './Crash.jsx'
import { installCrashHandlers } from './crash.js'

installCrashHandlers('main')
ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <CrashBoundary page="main">
      <App />
    </CrashBoundary>
  </React.StrictMode>,
)
