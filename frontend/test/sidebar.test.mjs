import test from 'node:test'
import assert from 'node:assert/strict'
import { SIDEBAR_KEY, readCollapsed, writeCollapsed } from '../src/sidebar.js'
import { menuStep, trapStep } from '../src/focus.js'
import { barTitleKey, canStartCall, networkLine, stateTone } from '../src/shell.js'
import { avatarColor, AVATAR_COLORS } from '../src/avatars.js'
import { translate } from '../src/i18n.js'

const memory = () => {
  const data = new Map()
  return { getItem: (k) => data.get(k) ?? null, setItem: (k, v) => data.set(k, String(v)), data }
}
const broken = { getItem: () => { throw new Error('blocked') }, setItem: () => { throw new Error('blocked') } }

test('the sidebar is expanded until it was collapsed, and the choice is remembered', () => {
  const store = memory()
  assert.equal(SIDEBAR_KEY, 'kolo-meet.sidebar')
  assert.equal(readCollapsed(store), false)
  writeCollapsed(true, store)
  assert.equal(store.data.get(SIDEBAR_KEY), 'collapsed')
  assert.equal(readCollapsed(store), true)
  writeCollapsed(false, store)
  assert.equal(readCollapsed(store), false)
})

test('without storage, or with storage that throws, the sidebar works and is expanded', () => {
  assert.equal(readCollapsed(null), false)
  assert.equal(readCollapsed(broken), false)
  assert.doesNotThrow(() => writeCollapsed(true, broken))
  assert.doesNotThrow(() => writeCollapsed(true, null))
  assert.equal(readCollapsed(), false, 'no localStorage in this process')
})

test('Tab stays inside a dialog: it wraps from the last element to the first and back', () => {
  assert.equal(trapStep(2, 3, false), 0)
  assert.equal(trapStep(0, 3, true), 2)
  assert.equal(trapStep(1, 3, false), -1, 'inside, the browser moves it')
  assert.equal(trapStep(-1, 3, false), 0, 'from outside it comes in')
  assert.equal(trapStep(-1, 3, true), 2)
  assert.equal(trapStep(0, 0, false), -1)
})

test('the arrow keys of the menu wrap, Home and End jump, other keys do nothing', () => {
  assert.equal(menuStep(0, 2, 'ArrowDown'), 1)
  assert.equal(menuStep(1, 2, 'ArrowDown'), 0)
  assert.equal(menuStep(0, 2, 'ArrowUp'), 1)
  assert.equal(menuStep(-1, 2, 'ArrowDown'), 0)
  assert.equal(menuStep(1, 2, 'Home'), 0)
  assert.equal(menuStep(0, 2, 'End'), 1)
  assert.equal(menuStep(0, 2, 'a'), -1)
  assert.equal(menuStep(0, 0, 'ArrowDown'), -1)
})

test('the color of a person follows the id: the same id gets the same color', () => {
  assert.equal(avatarColor('a1b2c3d4'), avatarColor('a1b2c3d4'))
  assert.ok(AVATAR_COLORS.includes(avatarColor('7f3e9c21')))
  assert.ok(AVATAR_COLORS.includes(avatarColor('')))
})

test('the network is one line: which network, then how it stands, and never an address', () => {
  const en = (k) => translate('en', k)
  assert.equal(networkLine(en, 'public', '3 in call'), 'Public network · 3 in call')
  assert.equal(networkLine(en, 'own', 'Ready'), 'Your own network · Ready')
  assert.equal(networkLine((k) => translate('es', k), 'local', 'Listo'), 'Solo este equipo · Listo')
  assert.equal(networkLine((k) => translate('pt', k), 'local', 'Pronto'), 'Só este computador · Pronto')
  assert.equal(networkLine(en, '', 'Starting'), 'Starting', 'before the mode is known only the state shows')
  assert.equal(networkLine(en, '203.0.113.10:4000', ''), '', 'an unknown mode says nothing')
})

test('a network that is up and waiting is fine; starting, connecting and errors stand out', () => {
  const ready = { state: 'ready' }
  assert.equal(stateTone({ tone: 'waiting' }, ready, null, false), 'live')
  assert.equal(stateTone({ tone: 'waiting' }, { state: 'starting' }, null, false), 'waiting')
  assert.equal(stateTone({ tone: 'waiting' }, ready, null, true), 'waiting', 'a call that connects')
  assert.equal(stateTone({ tone: 'live' }, ready, null, false), 'live')
  assert.equal(stateTone({ tone: 'error' }, ready, { reason: 'data-dir-open' }, false), 'error')
})

test('the title of the bar follows the screen, and every key exists in the three languages', () => {
  const views = ['home', 'lobby', 'joining', 'call', 'ended', 'name', 'fatal', 'loading']
  assert.deepEqual(views.map(barTitleKey), ['bar.home', 'bar.new', 'bar.call', 'bar.call', 'bar.call', 'app.title', 'app.title', 'app.title'])
  for (const view of views) {
    for (const locale of ['en', 'es', 'pt']) {
      assert.notEqual(translate(locale, barTitleKey(view)), barTitleKey(view), `${view} in ${locale}`)
    }
  }
})

test('a call can start only with a ready network, a name and no call running', () => {
  assert.equal(canStartCall({ ready: true, named: true, inCall: false }), true)
  assert.equal(canStartCall({ ready: false, named: true, inCall: false }), false)
  assert.equal(canStartCall({ ready: true, named: false, inCall: false }), false)
  assert.equal(canStartCall({ ready: true, named: true, inCall: true }), false)
  assert.equal(canStartCall({}), false)
})
