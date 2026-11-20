import test from 'node:test'
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { transformSync } from 'rolldown/utils'

// The pages are JSX: they are compiled here with the compiler of the build (rolldown, which vite runs). The service
// talks to test/fixtures/backend.mjs instead of the bindings, and styles are left out.
const backendURL = new URL('./fixtures/backend.mjs', import.meta.url).href
registerHooks({
  resolve(specifier, context, next) {
    if (specifier === './wails.js') {
      return { url: backendURL, shortCircuit: true }
    }
    if (specifier.endsWith('.css')) {
      return { url: 'data:text/javascript,', shortCircuit: true }
    }
    return next(specifier, context)
  },
  load(url, context, next) {
    if (url.endsWith('.jsx') || url.endsWith('/src/service.js')) {
      const path = fileURLToPath(url)
      const source = readFileSync(path, 'utf8').replace('import.meta.env.DEV', 'false')
      return { format: 'module', source: transformSync(path, source, { jsx: { runtime: 'automatic' } }).code, shortCircuit: true }
    }
    return next(url, context)
  },
})

// The window of a page: its handlers are kept to be called by the tests.
const handlers = {}
globalThis.window = {
  navigator: { languages: ['en-US'] },
  location: { search: '' },
  localStorage: { getItem: () => null, setItem: () => {} },
  addEventListener: (name, fn) => (handlers[name] = [...(handlers[name] ?? []), fn]),
  removeEventListener: () => {},
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
}

// A server render says once that layout effects do not run there; the grid measures itself in one.
const consoleError = console.error
console.error = (...args) => String(args[0]).startsWith('Warning: useLayoutEffect does nothing on the server') || consoleError(...args)

const { createElement: h } = await import('react')
const { renderToStaticMarkup } = await import('react-dom/server')
const backend = await import('./fixtures/backend.mjs')
const { call, on } = await import('../src/service.js')
const { LIST_FIELDS, LIST_PAYLOADS } = await import('../src/lists.js')
const { CrashBoundary, CrashPane } = await import('../src/Crash.jsx')
const { crashed, installCrashHandlers, onCrash, refreshRunning, running, runningOf, stopRunning } = await import('../src/crash.js')
const { MAX_REPORT, errorReport, reportText } = await import('../src/report.js')
const { callReducer, noCall } = await import('../src/calls.js')
const { CallView } = await import('../src/CallView.jsx')
const { Joining } = await import('../src/Joining.jsx')
const { Ended } = await import('../src/Ended.jsx')
const { Home } = await import('../src/Home.jsx')
const { HistoryList } = await import('../src/History.jsx')
const { SettingsPanel } = await import('../src/Settings.jsx')
const { Lobby } = await import('../src/Lobby.jsx')

installCrashHandlers('main')

const tick = () => new Promise((resolve) => setTimeout(resolve, 0))
const nothing = () => {}
const OFF = { state: 'off', deviceId: '', error: null }
const ON = { state: 'on', deviceId: '', error: null }

/**
 * What a page shows: its markup, or what CrashBoundary shows instead when its render throws. React renders the
 * boundary again with getDerivedStateFromError's state; a server render runs no boundary, so this does it.
 */
function page(children, name = 'main') {
  const boundary = new CrashBoundary({ page: name, children })
  try {
    return renderToStaticMarkup(boundary.render())
  } catch (error) {
    boundary.state = { ...boundary.state, ...CrashBoundary.getDerivedStateFromError(error) }
    return renderToStaticMarkup(boundary.render())
  }
}

// Every payload of an event or a method of the service, with null for each of its lists.
const call0 = { id: 'a', seq: 1, state: 'active', role: 'host', phase: '', participants: null, microphone: ON, camera: OFF, speaker: ON, security: { header: '' }, started: 1, finished: 0, error: null, warning: null }
const network0 = { state: 'ready', message: '', error: null, mode: 'public', entryNodes: 2, firstNode: '', relays: null, seq: 1 }
const nulls = {
  'meet:call': call0,
  'meet:network': network0,
  'meet:history': null,
  'meet:levels': { levels: null },
  'meet:lobby': { microphone: OFF, camera: OFF, speaker: OFF },
  'meet:participant': { kind: 'left', id: 'b', name: 'Bia', message: '' },
  'meet:notch': { active: true, state: 'active', role: 'guest', participants: 2 },
  Start: { dataDir: '/tmp/x', version: 'v0', network: network0, keyId: '' },
  Info: { dataDir: '/tmp/x', version: 'v0', network: network0, keyId: '' },
  Call: call0,
  CreateCall: call0,
  JoinCall: { ...call0, state: 'joining', role: 'guest' },
  Devices: { cameras: null, microphones: null, speakers: null },
  History: null,
}

/** Every list field in a payload that is not a list. */
function nullLists(value, path = '') {
  if (Array.isArray(value)) {
    return value.flatMap((x, i) => nullLists(x, `${path}[${i}]`))
  }
  if (!value || typeof value !== 'object') {
    return []
  }
  return Object.entries(value).flatMap(([k, v]) => (LIST_FIELDS.includes(k) && !Array.isArray(v) ? [`${path}.${k}`] : nullLists(v, `${path}.${k}`)))
}

test('every list field of the Go types is one the window makes a list', () => {
  const root = fileURLToPath(new URL('../../', import.meta.url))
  const sources = existsSync(`${root}call.go`) ? readdirSync(root).filter((n) => n.endsWith('.go') && !n.endsWith('_test.go')) : []
  for (const name of sources) {
    for (const m of readFileSync(`${root}${name}`, 'utf8').matchAll(/^\s+\w+\s+(?:\[\](?!byte\b)|map\[)\S+\s+`json:"(\w+)"/gm)) {
      assert.ok(LIST_FIELDS.includes(m[1]), `${name}: ${m[1]}`)
    }
  }
})

test('null for every list of every event and answer reaches the window as a list', async () => {
  const got = {}
  for (const name of Object.keys(nulls).filter((n) => n.startsWith('meet:'))) {
    const off = on(name, (data) => (got[name] = data))
    await tick()
    backend.emit(name, structuredClone(nulls[name]))
    off()
  }
  for (const name of Object.keys(nulls).filter((n) => !n.startsWith('meet:'))) {
    backend.answers[name] = nulls[name]
    got[name] = await call(name)
  }
  for (const [name, data] of Object.entries(got)) {
    assert.deepEqual(nullLists(data), [], name)
    if (LIST_PAYLOADS.includes(name)) {
      assert.deepEqual(data, [], name)
    }
  }
  assert.deepEqual(got['meet:call'].participants, [])
  assert.deepEqual(got.Start.network.relays, [])
  assert.deepEqual(got.Devices, { cameras: [], microphones: [], speakers: [] })

  // Each view shows itself with what arrived, not the error pane and not nothing.
  const settings = { displayName: 'Ana', networkMode: 'public', entryNodes: '', entryNodesFrom: 'public', connectionRelays: '', connectionRelaysFrom: 'none', firstNode: '', dataDir: '/tmp/x', maxParticipants: 8, showInNotch: true, cameraId: '', microphoneId: '', speakerId: '' }
  const views = [
    ['call', h(CallView, { call: callReducer(noCall, { type: 'call', call: got['meet:call'] }).call, relays: got['meet:network'].relays, onToggle: nothing, onLeave: nothing, onInvite: nothing, onSecurity: nothing, onHelp: nothing })],
    ['joining', h(Joining, { call: got.JoinCall, relays: got.Start.network.relays, onCancel: nothing })],
    ['ended', h(Ended, { call: { ...got.Call, state: 'ended', finished: 2 }, people: 0, history: got['meet:history'], onClear: nothing, onHome: nothing, onRetry: nothing, onNewInvite: nothing })],
    ['home', h(Home, { max: 8, ready: true, relays: '', history: got.History, onStart: nothing, onJoin: nothing, onClear: nothing })],
    ['history', h(HistoryList, { items: got.History })],
    ['settings', h(SettingsPanel, { settings, devices: got.Devices, network: got['meet:network'], version: 'v0', language: 'en', theme: 'system', onLanguage: nothing, onTheme: nothing, onName: nothing, onDevices: nothing, onRefresh: nothing, onNotch: nothing, onCopy: nothing })],
    ['lobby', h(Lobby, { intent: 'start', settings, devices: got.Devices, lobby: got['meet:lobby'], ready: true, relays: '', access: 'authorized', onToggle: nothing, onDevices: nothing, onRefresh: nothing, onBegin: nothing, onBack: nothing, onChangeInvite: nothing, onHelp: nothing, onAsk: nothing, onCancelAsk: nothing })],
  ]
  for (const [name, view] of views) {
    const html = page(view)
    assert.ok(html.length > 0 && !html.includes('class="crash'), `${name}: ${html.slice(0, 300)}`)
  }
})

test('a page whose render throws shows the error pane with Copy, and Leave while the call runs', async () => {
  backend.emit('meet:call', { ...call0, participants: [] })
  assert.deepEqual(running(), { call: 'host', devices: false })
  // The view itself reads the people as a list: the null that reached it unguarded stops the render.
  const html = page(h(CallView, { call: call0, relays: [], onToggle: nothing, onLeave: nothing }))
  assert.match(html, /class="crash" role="alert" lang="en"/)
  assert.match(html, /This window stopped on an error/)
  assert.match(html, /TypeError: /)
  assert.match(html, /Copy the error/)
  assert.match(html, /Your call is still running/)
  assert.match(html, />End call</)

  backend.emit('meet:call', { ...call0, seq: 2, role: 'guest', participants: [] })
  assert.match(page(h(() => null.x)), />Leave</)
  // An older answer of Call() does not undo a newer event.
  backend.answers.Call = { ...call0, seq: 1, state: 'ended', participants: [] }
  await refreshRunning('main')
  assert.equal(running().call, 'guest')

  backend.calls.length = 0
  await stopRunning('main', 'call')
  assert.deepEqual(backend.calls[0], ['LeaveCall'])
  backend.emit('meet:call', { ...call0, seq: 3, state: 'ended', participants: [] })
  const after = page(h(() => null.x))
  assert.ok(!after.includes('Leave') && !after.includes('End call') && after.includes('Copy the error'))
})

test('with the camera or microphone of the lobby on, the pane can turn them off', async () => {
  backend.emit('meet:lobby', { microphone: ON, camera: OFF, speaker: OFF })
  assert.deepEqual(running(), { call: '', devices: true })
  assert.match(page(h(() => null.x)), /Your camera or microphone is still on.*Turn them off/s)
  backend.calls.length = 0
  await stopRunning('main', 'devices')
  assert.deepEqual(backend.calls.slice(0, 2), [['SetCamera', false], ['SetMicrophone', false]])
  backend.emit('meet:lobby', { microphone: OFF, camera: OFF, speaker: OFF })
  assert.deepEqual(running(), { call: '', devices: false })
})

test('the notch pane fits one row and keeps Leave, which leaves through the notch', async () => {
  assert.deepEqual(runningOf({ call: { active: true, state: 'active', role: 'guest' } }), { call: 'guest', devices: false })
  assert.deepEqual(runningOf({ call: { active: false, state: 'ended', role: 'guest' } }), { call: '', devices: false })
  assert.deepEqual(runningOf({ call: { state: 'connecting', role: 'guest' } }).call, 'guest')
  backend.emit('meet:call', { ...call0, id: 'n', seq: 1, role: 'guest', participants: [] })
  const html = page(h(() => null.x), 'notch')
  assert.match(html, /class="crash crash--notch"/)
  assert.match(html, />Leave</)
  assert.ok(!html.includes('crash__text'), 'the error text stays off the small notch')
  backend.calls.length = 0
  await stopRunning('notch', 'call')
  assert.deepEqual(backend.calls[0], ['NotchLeave'])
  backend.emit('meet:call', { ...call0, id: 'n', seq: 2, state: 'ended', participants: [] })
})

test('an error outside a render is reported, shown, and the pane comes over the page', async () => {
  const shown = []
  const off = onCrash((text) => shown.push(text))
  backend.calls.length = 0
  handlers.error.forEach((fn) => fn({ error: new TypeError('boom'), message: 'boom' }))
  handlers.error.forEach((fn) => fn({ error: null, message: 'ResizeObserver loop completed with undelivered notifications.' }))
  handlers.unhandledrejection.forEach((fn) => fn({ reason: { reason: 'no-call', message: 'Ana left koloinv0ABC' } }))
  off()
  await tick()
  await tick()
  assert.equal(shown.length, 2)
  assert.match(shown[0], /^TypeError: boom\n/)
  assert.equal(shown[1], 'MeetError: no-call')
  const reports = backend.calls.filter(([name]) => name === 'ReportError')
  assert.equal(reports.length, 2)
  assert.equal(reports[0][1], 'TypeError: boom')
  assert.match(reports[0][2], /crash\.test\.mjs/)
  assert.deepEqual(reports[1].slice(1), ['MeetError: no-call', ''])
  // After an error the window asks what still runs.
  assert.ok(backend.calls.some(([name]) => name === 'Call') && backend.calls.some(([name]) => name === 'Lobby'))

  const pane = renderToStaticMarkup(h(CrashPane, { text: shown[0], page: 'main', onDismiss: nothing }))
  assert.match(pane, /Copy the error.*Dismiss/s)
  assert.equal(crashed('main', new Error('x')).split('\n')[0], 'Error: x')
})

test('a report holds the error and its stack only, without secrets, within the bound', () => {
  const key = 'ab'.repeat(32)
  const r = errorReport(new Error(`bad koloinv0ABCDEF for ${key}`), '\n    at CallView\n    at App')
  assert.equal(r.message, 'Error: bad [invite] for [key]')
  assert.match(r.stack, /at CallView\n {4}at App$/)
  assert.ok(!r.stack.includes('koloinv0') && !r.stack.includes(key))
  assert.match(reportText(errorReport(new Error('img data:image/jpeg;base64,AAAA'))), /^Error: img \[picture\]/)
  assert.equal(errorReport('Ana').message, 'Error: a thrown string')
  assert.equal(errorReport(null).message, 'Error: a thrown null')
  assert.equal(errorReport({ cause: '{"reason":"call-full","message":"Ana"}' }).message, 'MeetError: call-full')
  const long = errorReport(new Error('é'.repeat(MAX_REPORT * 2)))
  assert.equal(Array.from(reportText(long)).length, MAX_REPORT)
})

test('a call with null participants reads as a call without people', () => {
  const s = callReducer(noCall, { type: 'call', call: { id: 'a', seq: 1, state: 'joining', participants: null } })
  assert.deepEqual(s.call.participants, [])
  assert.deepEqual(callReducer(noCall, { type: 'call', call: { id: 'b', seq: 1, state: 'joining' } }).call.participants, [])
})
