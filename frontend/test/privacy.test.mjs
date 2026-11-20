import test from 'node:test'
import assert from 'node:assert/strict'
import { readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const src = fileURLToPath(new URL('../src/', import.meta.url))
const read = (name) => readFileSync(`${src}${name}`, 'utf8')
const sources = readdirSync(src).filter((name) => /\.(jsx?|css)$/.test(name))

test('the notch page never holds an invite', () => {
  const text = read('Notch.jsx').replace(/hasInvite|NotchCopyInvite|notch\.copyInvite|never holds the invite/g, '')
  assert.ok(!/invite/i.test(text))
  assert.ok(!/copyText|Clipboard/.test(text))
})

test('the invite is on screen in its dialog only, and is never stored', () => {
  for (const name of sources) {
    const text = read(name)
    // calls.js only puts a new invite into the copy of the running call.
    if (name !== 'Dialogs.jsx' && name !== 'App.jsx' && name !== 'calls.js' && !name.endsWith('.css')) {
      assert.ok(!/\.invite\b(?!')/.test(text), `${name} reads an invite`)
    }
    assert.ok(!/localStorage|sessionStorage|indexedDB|document\.cookie/.test(text) || name === 'prefs.js' || name === 'sidebar.js', `${name} stores something`)
  }
  assert.ok(!/invite/i.test(read('prefs.js').replace('Never an invite', '')))
  // App.jsx hands the invite to the dialog and to the clipboard, and puts it nowhere else.
  const uses = read('App.jsx').split('\n').filter((line) => /current\??\.invite/.test(line))
  for (const line of uses) {
    assert.ok(/InviteDialog|copyText|dialog === 'invite'|current\.invite &&/.test(line), line.trim())
  }
  assert.ok(!/document\.title = .*invite|location|console\./.test(read('App.jsx')))
})

test('nothing reads a camera or a microphone, and nothing leaves the app', () => {
  const mock = readFileSync(fileURLToPath(new URL('../dev/mock.ts', import.meta.url)), 'utf8')
  for (const [name, text] of [...sources.map((name) => [name, read(name)]), ['dev/mock.ts', mock]]) {
    assert.ok(!/getUserMedia|mediaDevices|getDisplayMedia|AudioContext|MediaRecorder/.test(text), `${name} reaches for a device`)
    assert.ok(!/\bfetch\(|XMLHttpRequest|WebSocket|sendBeacon|@import|url\(\s*['"]?https?:/.test(text), `${name} reaches for the network`)
    assert.ok(!/innerHTML|outerHTML|dangerouslySetInnerHTML|insertAdjacentHTML/.test(text), `${name} writes HTML from a string`)
  }
})

test('names and device names reach the screen through safeText', () => {
  assert.ok(read('App.jsx').includes('name={safeText(settings?.displayName)}'), 'the name in the card of the sidebar goes through safeText')
  assert.match(read('Avatar.jsx'), /import \{ initial \} from '\.\/format\.js'/)
  assert.ok(/\{initial\(name\)\}/.test(read('Avatar.jsx')), 'an Avatar draws initial(name)')
  assert.ok(/export function initial[^]*safeText\(name\)/.test(read('format.js')), 'initial reads safeText')
  for (const [name, fields] of [['CallView.jsx', ['person.name']], ['Notch.jsx', ['s.speaker']], ['App.jsx', ['toast.name']], ['Dialogs.jsx', ['security.operator']], ['parts.jsx', ['d.name']]]) {
    const text = read(name)
    for (const field of fields) {
      const escaped = field.replace('.', '\\.')
      const all = text.match(new RegExp(`(?<!['\\w])${escaped}\\b`, 'g')) ?? []
      // An Avatar is given the raw name and draws its initial through initial(), which reads safeText (checked below).
      const safe = text.match(new RegExp(`(safeText|initial)\\(${escaped}\\)|<Avatar [^>]*name=\\{${escaped}\\}`, 'g')) ?? []
      assert.ok(all.length > 0, `${name} shows ${field}`)
      assert.equal(safe.length, all.length, `${name}: every ${field} goes through safeText`)
    }
  }
})
