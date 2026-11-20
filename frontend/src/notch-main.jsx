import React from 'react'
import ReactDOM from 'react-dom/client'
import { Notch } from './Notch.jsx'
import { housingOf, showHousing } from './housing.js'
import { CrashBoundary } from './Crash.jsx'
import { installCrashHandlers } from './crash.js'

showHousing(document.documentElement, housingOf(window.location.search))
installCrashHandlers('notch')

ReactDOM.createRoot(document.getElementById('root')).render(
  <React.StrictMode>
    <CrashBoundary page="notch">
      <Notch />
    </CrashBoundary>
  </React.StrictMode>,
)
