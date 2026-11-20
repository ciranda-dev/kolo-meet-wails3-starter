import { call, on } from './service.js'
import { errorReport, reportText } from './report.js'

const LIVE = ['creating', 'joining', 'connecting', 'active']

/**
 * What runs in Go that an error pane must let the person stop: a call ('host' or 'guest', from a `Call` or a
 * `NotchState`), else the camera or the microphone of the lobby.
 */
export function runningOf({ call: c, lobby } = {}) {
  if (LIVE.includes(c?.state)) {
    return { call: c.role === 'host' ? 'host' : 'guest', devices: false }
  }
  return { call: '', devices: ['camera', 'microphone'].some((d) => lobby?.[d]?.state === 'on') }
}

// The newest call and lobby, kept outside React so that an error pane still knows them once the page is gone. Only
// what runningOf reads is kept: never the invite.
let seen = {}
let now = runningOf()
const watchers = new Set()

function keep(patch) {
  seen = { ...seen, ...patch }
  const next = runningOf(seen)
  if (next.call !== now.call || next.devices !== now.devices) {
    now = next
    watchers.forEach((fn) => fn())
  }
}

const slim = (c) => (c && typeof c.state === 'string' ? { id: c.id, seq: c.seq, state: c.state, role: c.role } : null)

// A call answer older than the event already kept (same call, lower seq) is dropped.
function keepCall(c) {
  const next = slim(c)
  const old = seen.call
  if (next && old && next.id && next.id === old.id && next.seq < old.seq) {
    return
  }
  keep({ call: next })
}

/** What runs now, the same object until it changes (for useSyncExternalStore). */
export const running = () => now

/** Calls fn when what runs changes. Returns the function that stops it. */
export function watchRunning(fn) {
  watchers.add(fn)
  return () => watchers.delete(fn)
}

/** Asks the service what runs now: after an error the events alone may be late. */
export function refreshRunning(page) {
  if (page === 'notch') {
    return call('Notch').then(keepCall).catch(() => {})
  }
  return Promise.all([
    call('Call').then((c) => keepCall(c ?? {})).catch(() => {}),
    call('Lobby').then((lobby) => keep({ lobby })).catch(() => {}),
  ])
}

/** Leaves the call, or turns the devices of the lobby off: the methods the window and the notch use themselves. */
export function stopRunning(page, what) {
  const done = what === 'call'
    ? call(page === 'notch' ? 'NotchLeave' : 'LeaveCall')
    : Promise.all([call('SetCamera', false), call('SetMicrophone', false)])
  return done.finally(() => refreshRunning(page))
}

/** Sends a report to the Go log (the terminal that started the app): the error and its stack only. */
export function report(r) {
  return call('ReportError', r.message, r.stack).catch(() => {})
}

const listeners = new Set()

/** Calls fn with the text of every error the page did not handle (window.onerror, unhandledrejection). */
export function onCrash(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** A page stopped on an error: report it, show it to fn's of onCrash, and learn what still runs. */
export function crashed(page, error, componentStack = '') {
  const r = errorReport(error, componentStack)
  report(r)
  refreshRunning(page)
  const text = reportText(r)
  listeners.forEach((fn) => fn(text))
  return text
}

/** Installs, once per page ('main' or 'notch'), the window-wide error handlers and the tracking of what runs. */
export function installCrashHandlers(page) {
  if (page === 'notch') {
    on('meet:notch', keepCall)
  } else {
    on('meet:call', keepCall)
    on('meet:lobby', (lobby) => keep({ lobby }))
  }
  window.addEventListener('error', (ev) => {
    // The browser reports a resize that did not settle in one frame as an error: nothing failed.
    if (ev.error == null && /^ResizeObserver loop/.test(ev.message ?? '')) {
      return
    }
    crashed(page, ev.error ?? new Error(String(ev.message)))
  })
  window.addEventListener('unhandledrejection', (ev) => crashed(page, ev.reason))
}
