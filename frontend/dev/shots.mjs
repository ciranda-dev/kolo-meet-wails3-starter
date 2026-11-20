// Screenshots of every state against the dev server and the mock, through Chrome's DevTools protocol.
// Usage: node dev/shots.mjs <out-dir> [base-url] [filter]. Needs Google Chrome; nothing to install.
// The mock draws its own test pictures, so no camera or microphone is opened.
import { spawn } from 'node:child_process'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const out = process.argv[2]
const base = process.argv[3] || 'http://127.0.0.1:9245'
const filter = process.argv[4] || ''
if (!out) {
  console.error('usage: node dev/shots.mjs <out-dir> [base-url] [filter]')
  process.exit(2)
}
const CHROME = process.env.CHROME || '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome'
// name: width, height, pixel ratio. The two large sizes are where a screen that does not fill the window shows; at 800 the sidebar shrinks, at 640 it is a rail.
const SIZES = { default: [1000, 618, 2], min: [640, 520, 2], narrow: [800, 600, 1], wide: [1440, 900, 1], xl: [2000, 1240, 1] }
const TRANSLATED = ['default', 'min']
// Checked without a picture: the largest window, where nothing may sit as an island in the middle.
const HUGE = [2560, 1440]
const PREFS = 'kolo-meet:prefs'
const click = (selector) => `document.querySelector(${JSON.stringify(selector)}).click()`
const button = (text) => `[...document.querySelectorAll('button')].find((b) => b.textContent.trim() === ${JSON.stringify(text)}).click()`
const type = (selector, text) => `(() => { const el = document.querySelector(${JSON.stringify(selector)}); const proto = el instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; Object.getOwnPropertyDescriptor(proto, 'value').set.call(el, ${JSON.stringify(text)}); el.dispatchEvent(new Event('input', { bubbles: true })) })()`
const startCall = click('.hero__actions .btn')
const joinCall = click('.hero__actions .btn--ghost')
const settings = `document.querySelectorAll('.dock__btn')[1].click()`
const historyPage = `document.querySelectorAll('.dock__btn')[0].click()`
const closeDialog = click('.modal__head .iconbtn')
// At 640 px the sidebar is a rail with no button: there is nothing to press.
const collapse = `document.querySelector('[aria-controls="calls-panel"]')?.click()`
const plus = click('.add button')
const choose = (n) => `document.querySelectorAll('[role=menuitem]')[${n}].click()`
const submitJoin = click('.joinform button[type=submit]')
const INVITE = 'koloinv0qx7Zt2mVb9LkR4sWd8YcA1fGh6JnP3uTe5XoBi0MyKvNrCa7DwEzQ2lHs4FgUj9pSx6Ot'

// No text, tooltip or label on screen may hold a character without a visible shape.
const HIDDEN = `(() => { const bad = /[\\p{Cc}\\p{Cf}\\p{Zl}\\p{Zp}\\p{Co}\\p{Cs}]/u; const texts = [document.body.innerText.replace(/[\\n\\t]/g, ' ')]; for (const el of document.querySelectorAll('[title], [aria-label]')) { texts.push((el.getAttribute('title') ?? '').replace(/\\n/g, ' '), el.getAttribute('aria-label') ?? '') } return texts.some((x) => bad.test(x)) ? 'an invisible or direction-changing character is on screen' : '' })()`
// The invite may be on screen in its two dialogs only, and never in the title or the address.
const LEAK = `(() => { const open = document.querySelector('.invite__value, #join-invite'); const text = document.body.innerText + document.title + location.href + [...document.querySelectorAll('[title], [aria-label]')].map((el) => (el.getAttribute('title') ?? '') + (el.getAttribute('aria-label') ?? '')).join(' '); return !open && /koloinv0\\w{8,}/.test(text) ? 'an invite is on screen outside its dialog' : '' })()`
// At 1440 px and wider a screen must fill its column: its panes together span at least 90% of the
// width of the main column and of the height under the bar, and no large rounded, bordered card sits inside a pane.
const FILLS = `(() => {
  if (window.innerWidth < 1440) return ''
  const room = document.querySelector('.grid') ?? document.querySelector('.modal--page .modal__body') ?? document.querySelector('.main')
  const column = document.querySelector('.main').getBoundingClientRect().width
  const scope = document.querySelector('.modal--page') ? '.modal--page .settings__group' : '.body [data-pane]'
  const rects = [...document.querySelectorAll(scope)].map((el) => el.getBoundingClientRect()).filter((r) => r.width > 0 && r.height > 0)
  if (rects.length === 0) return 'no pane on screen'
  const width = Math.max(...rects.map((r) => r.right)) - Math.min(...rects.map((r) => r.left))
  const height = Math.max(...rects.map((r) => r.bottom)) - Math.min(...rects.map((r) => r.top))
  if (width < 0.9 * column) return 'the content is ' + Math.round(100 * width / column) + '% of the width of the column'
  if (height < 0.9 * room.getBoundingClientRect().height) return 'the content is ' + Math.round(100 * height / room.getBoundingClientRect().height) + '% of the height it has'
  for (const el of document.querySelectorAll('.pane *, .lobby__side *, .modal--page .modal__body *')) {
    if (el.matches('.preview, .preview *, textarea, .tile, .tile *')) continue
    const style = getComputedStyle(el)
    const r = el.getBoundingClientRect()
    if (parseFloat(style.borderTopWidth) > 0 && parseFloat(style.borderTopLeftRadius) >= 12 && r.width > 480 && r.height > 240) return 'a card inside the page: ' + el.className
  }
  return ''
})()`
// No text may touch the edge of the surface it sits on.
const INSET = `(() => {
  for (const el of document.querySelectorAll('.bar *, .dock *, .main *, .modal *')) {
    if (![...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim() !== '')) continue
    if (el.closest('.sr-only, option, .tile__plate, .tile, .path__value')) continue
    const r = el.getBoundingClientRect()
    if (r.width < 2 || r.height < 2) continue
    const box = el.closest('.modal__box, [data-pane], .secbar, .controls, .bar, .dock')
    if (!box || box === el) continue
    const b = box.getBoundingClientRect()
    if (r.left - b.left < 8 || b.right - r.right < 4) return 'text touches the edge of its surface: ' + el.className + ' "' + el.textContent.trim().slice(0, 30) + '"'
  }
  return ''
})()`
const OVERFLOW = `(() => { if (document.documentElement.scrollWidth > window.innerWidth) return 'page scrolls sideways'; for (const el of document.querySelectorAll('.tile, .controls > *, .secbar, .side, .bar, .notch > *')) { const r = el.getBoundingClientRect(); if (r.right > window.innerWidth + 0.5 || r.bottom > window.innerHeight + 0.5) return 'something is outside the window: ' + el.className } return '' })()`

/** name, mock scene, steps to run after load, options: lang, only (one size), os. */
const SHOTS = [
  ['name', 'name', []],
  ['name-empty', 'name', [click('button[type=submit]')]],
  ['home', 'home', []],
  ['home-empty', 'home-empty', []],
  ['home-starting', 'starting', []],
  ['home-public-unreachable', 'public-unreachable', []],
  ['home-network-failed', 'net-failed', []],
  ['home-first-in-use', 'first-in-use', []],
  ['fatal', 'fatal', []],
  ['join-dialog', 'home', [joinCall]],
  ['join-dialog-pasted', 'home', [joinCall, button('Paste')]],
  ['join-dialog-empty', 'home', [joinCall, submitJoin]],
  ['join-dialog-invalid', 'home', [joinCall, type('#join-invite', 'hello, are you there'), submitJoin]],
  ['lobby-start', 'home', [startCall]],
  ['lobby-on', 'lobby-on', [startCall]],
  ['lobby-join', 'lobby-on', [joinCall, type('#join-invite', INVITE), submitJoin]],
  ['lobby-denied-mac', 'lobby-denied', [startCall], { os: 'MacIntel' }],
  ['lobby-denied-windows', 'lobby-denied', [startCall], { os: 'Win32' }],
  ['lobby-denied-linux', 'lobby-denied', [startCall], { os: 'Linux x86_64' }],
  ['lobby-no-device', 'lobby-none', [startCall]],
  ['lobby-camera-lost', 'lobby-lost', [startCall]],
  ['lobby-microphone-ask', 'lobby-ask', [startCall]],
  ['lobby-microphone-asking', 'lobby-ask', [startCall, button('Allow the microphone')]],
  ['lobby-microphone-restricted', 'lobby-restricted', [startCall]],
  ['lobby-microphone-silent', 'lobby-silent', [startCall]],
  ['lobby-name-empty', 'home', [startCall, type('#lobby-name', '   '), click('.lobby button[type=submit]')]],
  ['creating', 'creating', []],
  ['joining-finding-host', 'joining', []],
  ['joining-waiting-for-host', 'waiting-host', []],
  ['connecting-relay', 'connecting', []],
  ['call-1-host-invite', 'call-1', []],
  ['call-1-host', 'call-1', [closeDialog]],
  ['call-1-guest', 'call-1-guest', []],
  ['call-2', 'call-2', []],
  ['call-3', 'call-3', []],
  ['call-4', 'call-4', []],
  ['call-5', 'call-5', []],
  ['call-6', 'call-6', []],
  ['call-7', 'call-7', []],
  ['call-8', 'call-8', []],
  ['call-states', 'call-states', []],
  ['call-hostile-names', 'call-hostile', []],
  ['call-device-errors', 'call-denied', []],
  ['call-device-help', 'call-denied', [button('How to fix')], { os: 'MacIntel' }],
  ['call-toast', 'call-toast', []],
  ['call-microphone-silent', 'call-silent', []],
  ['call-still-ending', 'call-stuck', [click('.controls .btn--danger')]],
  ['call-security-waiting', 'call-states', [click('.secbar__line')]],
  ['ended-call-full', 'ended-call-full', []],
  ['call-security-guest', 'call-4', [click('.secbar__line')]],
  ['call-security-host', 'call-3', [click('.secbar__line')]],
  ['call-security-unknown-mode', 'call-unknown-mode', [click('.secbar__line')]],
  ['call-focus', 'call-4', [`document.querySelector('.btn--device').focus()`]],
  ['call-invite', 'call-3', [button('Invite')]],
  ['ended-left', 'ended-left', []],
  ['ended-host-left', 'ended-call-ended-host-left', []],
  ['ended-lost', 'ended-call-ended-lost', []],
  ['ended-host-not-found', 'ended-host-not-found', []],
  ['ended-join-denied', 'ended-join-denied', []],
  ['ended-invite-expired', 'ended-invite-expired', []],
  ['ended-relay-unreachable', 'ended-relay-unreachable', []],
  ['ended-call-unavailable', 'ended-call-unavailable', []],
  ['history-panel', 'home', [historyPage]],
  ['history-panel-empty', 'home-empty', [historyPage]],
  ['collapsed-home', 'home', [collapse]],
  ['collapsed-call', 'call-4', [collapse]],
  ['add-menu', 'home', [plus]],
  ['add-menu-lobby', 'lobby-on', [startCall, plus]],
  ['join-from-menu', 'home', [plus, choose(1)]],
  ['join-from-menu-pasted', 'home', [plus, choose(1), button('Paste')]],
  ['start-from-menu', 'lobby-on', [plus, choose(0)]],
  ['home-own', 'own', []],
  ['home-local', 'local', []],
  ['call-own', 'call-own', []],
  ['call-local', 'call-local', []],
  ['es-collapsed-call', 'call-4', [collapse], { lang: 'es' }],
  ['es-add-menu', 'home', [plus], { lang: 'es' }],
  ['es-join-from-menu', 'home', [plus, choose(1)], { lang: 'es' }],
  ['pt-add-menu', 'home', [plus], { lang: 'pt' }],
  ['pt-join-from-menu', 'home', [plus, choose(1)], { lang: 'pt' }],
  ['pt-call-invite', 'call-3', [button('Convidar')], { lang: 'pt' }],
  ['settings', 'home', [settings]],
  ['settings-name-too-long', 'home', [settings, type('#settings-name', 'A name that goes on and on and on, far past what a tile can hold'), click('#settings-save-name')]],
  ['settings-flag', 'flag', [settings]],
  ['settings-public-unreachable', 'public-unreachable', [settings]],
  ['settings-public', 'home', [settings]],
  ['settings-own', 'own', [settings]],
  ['settings-local', 'local', [settings]],
  ['es-settings-local', 'local', [settings], { lang: 'es' }],
  ['pt-settings-local', 'local', [settings], { lang: 'pt' }],
  ['settings-in-call', 'call-relays', [settings]],
  ['home-relays', 'relays', []],
  ['settings-relays', 'relays', [settings]],
  ['settings-relays-flag', 'relays-flag', [settings]],
  ['joining-relays', 'joining-relays', []],
  ['call-relays', 'call-relays', []],
  ['call-relays-security', 'call-relays', [click('.secbar__line')]],
  ['call-relay-warning', 'call-relay-warning', []],
  ['ended-host-unreachable', 'ended-host-unreachable', []],
  ['ended-relay-no-common', 'ended-relay-no-common', []],
  ['es-settings-relays', 'relays', [settings], { lang: 'es' }],
  ['pt-call-relay-warning', 'call-relay-warning', [], { lang: 'pt' }],
  ['es-home', 'home', [], { lang: 'es' }],
  ['es-lobby-denied', 'lobby-denied', [startCall], { lang: 'es', os: 'MacIntel' }],
  ['es-call-states', 'call-states', [], { lang: 'es' }],
  ['es-call-security', 'call-4', [click('.secbar__line')], { lang: 'es' }],
  ['es-call-1-host-invite', 'call-1', [], { lang: 'es' }],
  ['es-ended-join-denied', 'ended-join-denied', [], { lang: 'es' }],
  ['es-settings', 'home', [settings], { lang: 'es' }],
  ['pt-home', 'home', [], { lang: 'pt' }],
  ['pt-lobby-on', 'lobby-on', [startCall], { lang: 'pt' }],
  ['pt-call-8', 'call-8', [], { lang: 'pt' }],
  ['pt-call-security', 'call-3', [click('.secbar__line')], { lang: 'pt' }],
  ['pt-joining', 'waiting-host', [], { lang: 'pt' }],
  ['pt-ended-host-left', 'ended-call-ended-host-left', [], { lang: 'pt' }],
  ['pt-settings', 'home', [settings], { lang: 'pt' }],
]

/** The notch page: name, mock scene, extra query. */
const NOTCH = [
  ['creating', 'creating', ''],
  ['joining', 'joining', ''],
  ['connecting', 'connecting', ''],
  ['alone-host', 'call-1', ''],
  ['speaking', 'call-4', ''],
  ['speaking-you', 'call-4', '&speaker=you'],
  ['quiet', 'call-4', '&speaker=none'],
  ['host-7', 'call-7', ''],
  ['guest-8', 'call-8', ''],
  ['devices-off', 'call-states', '&speaker=none'],
  ['hostile-name', 'call-hostile', ''],
  ['ended-left', 'ended-left', ''],
  ['ended-host-left', 'ended-call-ended-host-left', ''],
  ['ended-join-denied', 'ended-join-denied', ''],
  ['nothing', 'home', ''],
]

const profile = mkdtempSync(join(tmpdir(), 'kolo-meet-shots-'))
// The page never asks for a device; if it ever did, the request would be refused without a prompt.
const chrome = spawn(CHROME, ['--headless=new', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--hide-scrollbars', '--force-device-scale-factor=2', '--deny-permission-prompts', 'about:blank'], { stdio: ['ignore', 'ignore', 'pipe'] })
const wsUrl = await new Promise((resolve, reject) => {
  let text = ''
  chrome.stderr.on('data', (chunk) => {
    text += chunk
    const m = text.match(/DevTools listening on (ws:\/\/\S+)/)
    if (m) {
      resolve(m[1])
    }
  })
  chrome.on('exit', () => reject(new Error('Chrome stopped before it was ready')))
})
const port = new URL(wsUrl).port
const targets = await (await fetch(`http://127.0.0.1:${port}/json`)).json()
const ws = new WebSocket(targets.find((x) => x.type === 'page').webSocketDebuggerUrl)
await new Promise((resolve) => ws.addEventListener('open', resolve))
let seq = 0
const waiting = new Map()
const consoleErrors = []
ws.addEventListener('message', (event) => {
  const msg = JSON.parse(event.data)
  if (msg.id && waiting.has(msg.id)) {
    waiting.get(msg.id)(msg)
    waiting.delete(msg.id)
  } else if (msg.method === 'Runtime.exceptionThrown') {
    consoleErrors.push(msg.params.exceptionDetails.exception?.description ?? msg.params.exceptionDetails.text)
  } else if (msg.method === 'Runtime.consoleAPICalled' && msg.params.type === 'error') {
    consoleErrors.push(msg.params.args.map((a) => a.value ?? a.description ?? '').join(' '))
  }
})
const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const id = ++seq
    waiting.set(id, (msg) => (msg.error ? reject(new Error(`${method}: ${msg.error.message}`)) : resolve(msg.result)))
    ws.send(JSON.stringify({ id, method, params }))
  })
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
const evaluate = async (expression) => {
  const result = await send('Runtime.evaluate', { expression, awaitPromise: true })
  if (result.exceptionDetails) {
    throw new Error(`${expression.slice(0, 80)}: ${result.exceptionDetails.exception?.description ?? result.exceptionDetails.text}`)
  }
  return result.result.value
}

await send('Page.enable')
await send('Runtime.enable')
await send('Emulation.setFocusEmulationEnabled', { enabled: true })
const agent = (await send('Browser.getVersion')).userAgent
let count = 0
// The saved language is written before any script of the page runs, so one load is enough.
let languageScript = null
const setLanguage = async (lang) => {
  if (languageScript) {
    await send('Page.removeScriptToEvaluateOnNewDocument', { identifier: languageScript })
  }
  languageScript = (await send('Page.addScriptToEvaluateOnNewDocument', { source: `localStorage.setItem(${JSON.stringify(PREFS)}, JSON.stringify({ lang: ${JSON.stringify(lang)} })); localStorage.removeItem('kolo-meet.sidebar')` })).identifier
}
const problems = []
const check = async (label) => {
  for (const test of [OVERFLOW, HIDDEN, LEAK, FILLS, INSET]) {
    const found = await evaluate(test)
    if (found) {
      problems.push(`${label}: ${found}`)
    }
  }
  for (const text of consoleErrors.splice(0)) {
    problems.push(`${label}: console: ${text.slice(0, 200)}`)
  }
}
try {
  for (const [name, scene, steps, options = {}] of SHOTS) {
    if (filter && !name.includes(filter)) {
      continue
    }
    const lang = options.lang ?? 'en'
    for (const theme of ['dark', 'light']) {
      for (const [sizeName, [width, height, ratio]] of [...Object.entries(SIZES), ['huge', [...HUGE, 1]]]) {
        if (options.only ? options.only !== sizeName : lang !== 'en' && !TRANSLATED.includes(sizeName)) {
          continue
        }
        if (sizeName === 'huge' && theme !== 'dark') {
          continue
        }
        const os = options.os ?? 'MacIntel'
        await send('Emulation.setUserAgentOverride', { userAgent: agent, platform: os, userAgentMetadata: { platform: os.startsWith('Win') ? 'Windows' : os.startsWith('Linux') ? 'Linux' : 'macOS', platformVersion: '', architecture: '', model: '', mobile: false } })
        await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: ratio, mobile: false })
        await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: theme }, { name: 'prefers-reduced-motion', value: 'reduce' }] })
        await setLanguage(lang)
        consoleErrors.splice(0)
        await send('Page.navigate', { url: `${base}/?mock=${scene}&v=${count}` })
        for (let i = 0; i < 60 && !(await evaluate(`!!document.querySelector('.split, .lobby, .call')`)); i++) {
          await sleep(50)
        }
        await sleep(300)
        for (const step of steps) {
          await evaluate(step)
          await sleep(200)
        }
        await sleep(150)
        await check(`${name} ${theme} ${sizeName}`)
        if (sizeName !== 'huge') {
          const shot = await send('Page.captureScreenshot', { format: 'png' })
          const dir = join(out, theme, sizeName)
          mkdirSync(dir, { recursive: true })
          writeFileSync(join(dir, `${name}.png`), Buffer.from(shot.data, 'base64'))
        }
        count++
      }
    }
  }
  // The notch page: dark only, at the 660 x 92 content area of the notch window.
  for (const lang of ['en', 'es', 'pt']) {
    for (const [state, scene, extra] of NOTCH) {
      const name = `${lang === 'en' ? '' : `${lang}-`}${state}`
      if (filter && !`notch-${name}`.includes(filter)) {
        continue
      }
      await send('Emulation.setDeviceMetricsOverride', { width: 660, height: 92, deviceScaleFactor: 2, mobile: false })
      await send('Emulation.setEmulatedMedia', { features: [{ name: 'prefers-color-scheme', value: 'light' }, { name: 'prefers-reduced-motion', value: 'reduce' }] })
      await setLanguage(lang)
      consoleErrors.splice(0)
      await send('Page.navigate', { url: `${base}/notch/?mock=${scene}${extra}&v=${count}` })
      for (let i = 0; i < 40 && !(await evaluate(`!!document.querySelector('.notch')`)); i++) {
        await sleep(100)
      }
      await sleep(1200)
      const tall = await evaluate(`document.documentElement.scrollHeight > window.innerHeight ? 'content is taller than the notch area' : ''`)
      if (tall) {
        problems.push(`notch ${name}: ${tall}`)
      }
      // The notch page must never receive the invite, whatever the call is.
      const held = await evaluate(`/koloinv0/.test(document.documentElement.outerHTML) ? 'the notch page holds an invite' : ''`)
      if (held) {
        problems.push(`notch ${name}: ${held}`)
      }
      await check(`notch ${name}`)
      const shot = await send('Page.captureScreenshot', { format: 'png' })
      mkdirSync(join(out, 'notch'), { recursive: true })
      writeFileSync(join(out, 'notch', `${name}.png`), Buffer.from(shot.data, 'base64'))
      count++
    }
  }
} finally {
  ws.close()
  chrome.kill()
  await sleep(300)
  rmSync(profile, { recursive: true, force: true })
}
console.log(`${count} screenshots in ${out}`)
if (problems.length > 0) {
  console.log(problems.join('\n'))
  process.exitCode = 1
}
