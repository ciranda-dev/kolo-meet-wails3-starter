import test from 'node:test'
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { transformSync } from 'rolldown/utils'

// The pages are JSX, compiled here as in test/crash.test.mjs; the service talks to test/fixtures/backend.mjs.
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
globalThis.window ??= { navigator: { languages: ['en-US'] }, addEventListener: () => {}, removeEventListener: () => {}, setTimeout, clearTimeout, setInterval, clearInterval }
const consoleError = console.error
console.error = (...args) => String(args[0]).startsWith('Warning: useLayoutEffect does nothing on the server') || consoleError(...args)

const { createElement: h } = await import('react')
const { renderToStaticMarkup } = await import('react-dom/server')
const { I18n } = await import('../src/context.js')
const { translate } = await import('../src/i18n.js')
const { errorSentence } = await import('../src/errors.js')
const { asked, canRemove, peopleOf } = await import('../src/roster.js')
const { PeopleList } = await import('../src/People.jsx')
const { CallView } = await import('../src/CallView.jsx')
const { Ended } = await import('../src/Ended.jsx')
const { Joining } = await import('../src/Joining.jsx')

const nothing = () => {}
const OFF = { state: 'off', deviceId: '', error: null }
const me = { id: 'aa', name: 'Ana', self: true, host: true, state: 'live' }
const bruno = { id: 'bb', name: 'Bruno', self: false, host: false, state: 'live' }
const carla = { id: 'cc', name: 'Carla', self: false, host: false, state: 'live' }
const hostCall = { id: 'c1', seq: 3, state: 'active', role: 'host', participants: [me, bruno, carla], microphone: OFF, camera: OFF, speaker: OFF, security: { header: '' }, started: 1 }
const guestCall = { ...hostCall, role: 'guest', participants: [{ ...me, host: false }, { ...bruno, host: true }, carla] }

function render(node, locale = 'en') {
  const t = (key, vars) => translate(locale, key, vars)
  const value = { t, fail: (err, fallback) => errorSentence(t, locale, err, fallback), lang: locale }
  return renderToStaticMarkup(h(I18n.Provider, { value }, node))
}
const list = (props, locale) => render(h(PeopleList, { asking: '', removing: false, error: null, onAsk: nothing, onConfirm: nothing, onCancel: nothing, ...props }), locale)
const count = (html, text) => html.split(text).length - 1

test('only the host may remove, never itself or the host, and only in a running call', () => {
  assert.ok(canRemove(hostCall, bruno))
  assert.ok(!canRemove(hostCall, me))
  assert.ok(!canRemove(hostCall, { ...me, host: false }), 'the own row, even without the host mark')
  assert.ok(!canRemove(guestCall, carla))
  assert.ok(!canRemove(guestCall, guestCall.participants[1]))
  assert.ok(!canRemove({ ...hostCall, state: 'ended' }, bruno))
  assert.ok(!canRemove(hostCall, { ...bruno, host: true }))
  assert.ok(!canRemove(hostCall, null))
  assert.equal(asked(hostCall, 'bb'), bruno)
  assert.equal(asked(hostCall, 'aa'), null)
  assert.equal(asked(hostCall, 'gone'), null)
  assert.equal(asked(hostCall, ''), null)
  assert.equal(asked(guestCall, 'cc'), null)
})

test('the host sees Remove on each other person, a guest on nobody', () => {
  const host = list({ call: hostCall })
  assert.equal(count(host, '>Remove</button>'), 2)
  assert.match(host, /Ana \(you\)/)
  assert.match(host, /invites you have now no longer let that person in/)
  const guest = list({ call: guestCall })
  assert.equal(count(guest, '>Remove</button>'), 0)
  assert.doesNotMatch(guest, /invites you have now/)
  assert.match(guest, /Bruno/)
  assert.match(guest, /Carla/)
})

test('Remove asks first: the row asks with Cancel and Remove, and the others keep their button', () => {
  const html = list({ call: hostCall, asking: 'bb' })
  assert.match(html, /Remove Bruno from the call\?/)
  assert.match(html, />Cancel<\/button>/)
  assert.equal(count(html, 'btn--danger'), 1)
  assert.equal(count(html, '>Remove</button>'), 2) // Carla's Remove and the yes of Bruno's question
  assert.doesNotMatch(html, /Remove Carla/)
  const busy = list({ call: hostCall, asking: 'bb', removing: true })
  assert.match(busy, /disabled="">Removing<\/button>/)
  // The person left before the yes: the question goes with them.
  const gone = list({ call: { ...hostCall, participants: [me, carla] }, asking: 'bb' })
  assert.doesNotMatch(gone, /from the call\?/)
  assert.equal(list({ call: guestCall, asking: 'cc' }).includes('from the call?'), false)
  for (const [locale, words] of [['es', /¿Quitar a Bruno de la llamada\?/], ['pt', /Remover Bruno da chamada\?/]]) {
    assert.match(list({ call: hostCall, asking: 'bb' }, locale), words)
  }
})

test('a refused removal shows why, in the language of the window', () => {
  const error = { kind: 'refused', reason: 'not-host', detail: '', cause: '', message: 'Only the host can remove someone from the call.' }
  assert.match(list({ call: hostCall, error }), /role="alert">Only the host can remove someone from the call\./)
  assert.match(list({ call: hostCall, error }, 'pt'), /Só o anfitrião pode remover alguém da chamada\./)
})

test('null or missing lists read as nobody', () => {
  assert.deepEqual(peopleOf({ participants: null }), [])
  assert.deepEqual(peopleOf(null), [])
  assert.equal(asked({ ...hostCall, participants: null }, 'bb'), null)
  const html = list({ call: { ...hostCall, participants: null }, asking: 'bb' })
  assert.equal(count(html, '<li'), 0)
  assert.match(render(h(CallView, { call: { ...hostCall, participants: [] }, relays: [], onToggle: nothing, onLeave: nothing })), />End call<\/button>/)
})

test('the removed guest sees that the host removed it, in every language', () => {
  const error = { kind: 'ended', reason: 'call-ended-removed', detail: '', cause: '', message: 'The host removed you from this call.' }
  const call = { id: 'c2', state: 'ended', role: 'guest', participants: [], started: 1, finished: 2, error }
  const props = { call, people: 3, canRetry: true, history: [], onClear: nothing, onHome: nothing, onRetry: nothing, onNewInvite: nothing }
  for (const [locale, words] of [['en', 'The host removed you from this call.'], ['es', 'El anfitrión te quitó de esta llamada.'], ['pt', 'O anfitrião removeu você desta chamada.']]) {
    const html = render(h(Ended, props), locale)
    assert.ok(html.includes(`role="alert">${words}`), `${locale}: ${html}`)
    assert.doesNotMatch(html, /Try again|Intentar|Tentar/)
  }
  const refused = { ...call, started: 0, error: { kind: 'refused', reason: 'rejoin-refused', detail: '', cause: '', message: '' } }
  const html = render(h(Ended, { ...props, call: refused }))
  assert.match(html, /this invite no longer lets you in/)
  assert.match(html, />Use another invite</)
  assert.doesNotMatch(html, />Try again</)
})

test('a guest hears of a relay with a wrong key while it joins, and a join the relays could not save can be tried again', () => {
  const joining = { id: 'c3', state: 'joining', phase: 'finding-host', role: 'guest', participants: [] }
  const wrong = { kind: 'network', reason: 'connection-relay-wrong-key', detail: '127.0.0.1:7448', cause: '', message: '' }
  const relays = [{ addr: '127.0.0.1:7448', key: 'ab', state: 'not-reserved', error: wrong }]
  assert.match(render(h(Joining, { call: joining, relays, onCancel: nothing })), /The connection relay at 127\.0\.0\.1:7448 answered with another key\./)
  assert.match(render(h(Joining, { call: joining, relays, onCancel: nothing }), 'es'), /respondió con otra clave/)
  const fine = render(h(Joining, { call: joining, relays: [{ ...relays[0], state: 'idle', error: null }], onCancel: nothing }))
  assert.doesNotMatch(fine, /another key/)
  assert.match(fine, /tries your connection relays/)
  assert.doesNotMatch(render(h(Joining, { call: joining, relays: [], onCancel: nothing })), /connection relays/)
  const error = { kind: 'not-found', reason: 'host-unreachable-relays', detail: '', cause: '', message: '' }
  const html = render(h(Ended, { call: { id: 'c3', state: 'ended', role: 'guest', participants: [], started: 0, finished: 2, error }, people: 0, canRetry: true, history: [], onClear: nothing, onHome: nothing, onRetry: nothing, onNewInvite: nothing }))
  assert.match(html, /directly or through your connection relays/)
  assert.match(html, />Try again</)
})
