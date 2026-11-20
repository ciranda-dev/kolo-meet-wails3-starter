import test from 'node:test'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { hasChrome, openChrome, sleep } from '../dev/chrome.mjs'

// The shell of the window, served with dev/mock.ts in place of Go (loopback only) in a headless Chrome: the sidebar
// collapses and remembers it; the "+" menu and the dialog of its two rows take the keyboard and give the focus back; the
// actions of a call are all reachable; the network is one quiet line with no address; the tiles keep their shape.
const visible = (selector) => `(() => { const el = document.querySelector(${JSON.stringify(selector)}); return !!el && el.getClientRects().length > 0 })()`

test('the sidebar collapses, the add menu and its dialog use the keyboard, the actions of a call stay', { skip: !hasChrome() && 'Google Chrome is not here', timeout: 180000 }, async () => {
  const { createServer } = await import('vite')
  const server = await createServer({
    configFile: fileURLToPath(new URL('../vite.config.js', import.meta.url)),
    logLevel: 'silent',
    server: { host: '127.0.0.1', port: 0, strictPort: false },
  })
  await server.listen()
  const base = `http://127.0.0.1:${server.httpServer.address().port}`
  const { send, evaluate, until, close } = await openChrome(['--lang=en-US'])
  const open = async (path, ready) => {
    await send('Page.navigate', { url: base + path })
    assert.ok(await until(ready), `${path}: ${ready} never showed`)
  }
  const rect = (selector) => evaluate(`(() => { const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect(); return { left: r.left, right: r.right, width: r.width } })()`)
  const click = async (x, y) => {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y })
    await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 })
    await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 })
  }
  const CODES = { Enter: 13, Escape: 27, Tab: 9, ArrowDown: 40, ArrowUp: 38, Home: 36, End: 35 }
  const key = async (k, modifiers = 0) => {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, code: k, modifiers, windowsVirtualKeyCode: CODES[k], ...(k === 'Enter' ? { text: '\r' } : {}) })
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code: k, modifiers, windowsVirtualKeyCode: CODES[k] })
  }
  const focused = `document.activeElement.textContent.trim()`
  const inBox = `document.querySelector('.modal__box').contains(document.activeElement)`
  const plus = `document.querySelector('.add button')`
  const onPlus = `document.activeElement === ${plus}`
  const toggle = `document.querySelector('[aria-controls="calls-panel"]')`
  const inCall = visible('.grid .tile')
  const home = visible('.split')
  const noModal = `document.querySelector('.modal') === null`
  try {
    await send('Page.enable')
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

    // The shell with no call: the mark, CALLS with "+", a line that says there is no call, and the card with the name,
    // History, Settings and the network. The window has no bar of its own over the sidebar.
    await open('/?mock=home', home)
    assert.equal(await evaluate(`document.querySelector('.side__title').textContent`), 'Calls')
    assert.equal(await evaluate(`document.querySelectorAll('.sess').length`), 0, 'no call, no row')
    assert.equal(await evaluate(`document.querySelector('.side__empty').textContent.length > 0`), true)
    assert.equal(await evaluate(`document.querySelector('.dock__name').textContent`), 'Ana Ribeiro')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.dock__btn')].map((b) => b.textContent.trim())`), ['History', 'Settings'])
    assert.equal(await evaluate(`document.querySelector('.dock__net').textContent.trim()`), 'Public network · Ready')
    assert.equal(await evaluate(`document.querySelector('.bar__title').textContent`), 'Calls')
    assert.equal(await evaluate(`document.querySelector('header') === null`), true, 'the sidebar has the brand')

    // A call: one row for it, the state under its name, the people and the invite in the bar, the controls at the bottom.
    await open('/?mock=call-3', inCall)
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.sess')].map((b) => [b.querySelector('.sess__name').textContent, b.querySelector('.sess__line').textContent, b.getAttribute('aria-current')])`), [['This call', '3 in call', 'page']])
    assert.equal(await evaluate(`document.querySelector('.dock__net').textContent.trim()`), 'Public network · 3 in call')
    assert.equal(await evaluate(`${plus}.disabled`), true, 'a window holds one call: "+" waits')
    assert.equal(await evaluate(`document.querySelector('.bar__title').textContent`), 'Call')
    assert.equal(await evaluate(`document.querySelector('.bar__sub').textContent`), '3 in call')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.bar__actions .pill')].map((b) => b.textContent)`), ['People', 'Invite'], 'the host has People and Invite')
    const controls = await evaluate(`[...document.querySelectorAll('.controls .btn')].map((b) => b.textContent.trim())`)
    assert.deepEqual(controls, ['Mute', 'Stop camera', 'End call'], 'the microphone, the camera and the way out')
    assert.equal(await evaluate(`document.querySelector('.secbar__line') !== null`), true, 'the line about protection')
    await evaluate(`document.querySelector('.bar__actions .pill').click()`)
    assert.ok(await until(visible('.modal__box')), 'People opens its dialog')
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'People in the call')
    await key('Escape')
    assert.ok(await until(noModal), 'Escape closes it')
    await evaluate(`document.querySelectorAll('.bar__actions .pill')[1].click()`)
    assert.ok(await until(visible('.modal__box')), 'Invite opens its dialog')
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'Invite to this call')
    assert.equal(await evaluate(`document.querySelector('.invite__value').textContent.startsWith('koloinv0')`), true, 'the invite is in its dialog')
    assert.equal(await evaluate(`[...document.querySelectorAll('.modal .btn')].map((b) => b.textContent).slice(0, 2).join('|')`), 'Copy invite|Copy with instructions')
    await key('Escape')
    assert.ok(await until(noModal))
    await evaluate(`document.querySelector('.secbar__line').click()`)
    assert.ok(await until(visible('.modal__box')), 'the protection line opens its dialog')
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'Call protection')
    await key('Escape')
    assert.ok(await until(noModal))
    await open('/?mock=call-4', inCall)
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.bar__actions .pill')].map((b) => b.textContent)`), ['People'], 'a guest has no Invite')

    // The tiles keep their shape in the window: inside the grid, never a strip and never a column.
    for (const [width, height] of [[1440, 900], [2000, 1240], [800, 600]]) {
      await send('Emulation.setDeviceMetricsOverride', { width, height, deviceScaleFactor: 1, mobile: false })
      for (const scene of ['call-1', 'call-2', 'call-4', 'call-7']) {
        await open(`/?mock=${scene}`, inCall)
        await sleep(150)
        const bad = await evaluate(`(() => { const g = document.querySelector('.grid').getBoundingClientRect(); const out = []; for (const t of document.querySelectorAll('.grid .tile')) { const r = t.getBoundingClientRect(); const ratio = r.width / r.height; if (r.left < g.left - 1 || r.right > g.right + 1 || r.top < g.top - 1 || r.bottom > g.bottom + 1) out.push('outside the grid'); if (ratio > 2.45 || ratio < 0.69) out.push('shape ' + ratio.toFixed(2)) } if (document.documentElement.scrollWidth > innerWidth) out.push('scrolls sideways'); return out.join(', ') })()`)
        assert.equal(bad, '', `${scene} at ${width} x ${height}`)
      }
    }
    await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false })

    // The sidebar collapses to a rail: state, aria, the width given to the main column, the name of the call in its
    // label, the choice kept across a reload, and the keyboard.
    await open('/?mock=call-4', inCall)
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-expanded')`), 'true')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-label')`), 'Collapse the sidebar')
    const open0 = { side: (await rect('.side')).width, main: (await rect('.main')).width }
    await evaluate(`${toggle}.focus()`)
    await key('Enter')
    assert.ok(await until(`document.querySelector('.side.is-collapsed') !== null`), 'Enter on the toggle collapses it')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-expanded')`), 'false')
    assert.equal(await evaluate(`${toggle}.getAttribute('aria-label')`), 'Expand the sidebar')
    await sleep(300)
    const shut = { side: (await rect('.side')).width, main: (await rect('.main')).width }
    assert.ok(shut.side <= 80 && shut.main > open0.main + (open0.side - 80) - 2, `the main column takes the freed width: ${JSON.stringify({ open0, shut })}`)
    assert.equal(await evaluate(`getComputedStyle(document.querySelector('.sess__text')).display`), 'none', 'only the disc shows')
    assert.equal(await evaluate(`document.querySelector('.sess').getAttribute('aria-label')`), 'This call, 4 in call')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.dock__btn')].map((b) => [b.getAttribute('aria-label'), b.title])`), [['History', 'History'], ['Settings', 'Settings']], 'History and Settings keep their names')
    assert.equal(await evaluate(`localStorage.getItem('kolo-meet.sidebar')`), 'collapsed', 'the choice is stored')
    assert.equal(await evaluate(`${toggle}.matches(':focus-visible')`), true, 'the toggle keeps a visible focus')
    assert.equal(await evaluate(`document.querySelector('.dock__text').textContent`), 'Public network · 4 in call', 'the network line stays for a screen reader')
    assert.equal(await evaluate(`[...document.querySelectorAll('.tile')].every((t) => t.getBoundingClientRect().right <= innerWidth)`), true, 'the tiles use the freed width and stay inside')
    await open('/?mock=call-4', inCall)
    assert.ok(await evaluate(`document.querySelector('.side.is-collapsed') !== null`), 'a reload keeps it collapsed')
    await evaluate(`${toggle}.click()`)
    assert.ok(await until(`document.querySelector('.side.is-collapsed') === null`))
    assert.equal(await evaluate(`localStorage.getItem('kolo-meet.sidebar')`), 'expanded')
    // Storage that throws: the window still works, expanded.
    await send('Page.addScriptToEvaluateOnNewDocument', { source: `Object.defineProperty(window, 'localStorage', { get() { throw new Error('blocked') } })` })
    await open('/?mock=call-4', inCall)
    assert.equal(await evaluate(`document.querySelector('.side.is-collapsed') === null`), true, 'without storage it is expanded')
    await evaluate(`${toggle}.click()`)
    assert.ok(await until(`document.querySelector('.side.is-collapsed') !== null`), 'and it still collapses')
    await send('Page.navigate', { url: 'about:blank' })

    // The menu under "+": two rows with an icon, a title and a quiet line; arrows, Home, End, Enter, Escape and a click
    // outside; the focus goes back to the button.
    await open('/?mock=home', home)
    assert.equal(await evaluate(`${plus}.getAttribute('aria-haspopup')`), 'menu')
    assert.equal(await evaluate(`${plus}.getAttribute('aria-label')`), 'New call')
    assert.equal(await evaluate(`document.querySelector('.popover') === null`), true, 'closed at first')
    await evaluate(`${plus}.click()`)
    assert.ok(await until(visible('.popover')), 'the menu opens')
    assert.equal(await evaluate(`${plus}.getAttribute('aria-expanded')`), 'true')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('[role="menuitem"]')].map((i) => [i.querySelector('.choice__title').textContent, i.querySelector('.choice__sub').textContent.length > 0, i.querySelector('.choice__icon svg') !== null])`), [['Start a call', true, true], ['Join with an invite', true, true]])
    assert.match(await evaluate(focused), /^Start a call/, 'the first row has the focus')
    await key('ArrowDown')
    assert.match(await evaluate(focused), /^Join with an invite/)
    await key('ArrowDown')
    assert.match(await evaluate(focused), /^Start a call/, 'the arrows wrap')
    await key('ArrowUp')
    assert.match(await evaluate(focused), /^Join with an invite/)
    await key('Home')
    assert.match(await evaluate(focused), /^Start a call/)
    await key('End')
    assert.match(await evaluate(focused), /^Join with an invite/)
    await key('Escape')
    assert.ok(await until(`document.querySelector('.popover') === null`), 'Escape closes the menu')
    assert.equal(await evaluate(onPlus), true, 'the focus is back on the button')
    assert.equal(await evaluate(`${plus}.getAttribute('aria-expanded')`), 'false')
    await key('ArrowDown')
    assert.ok(await until(visible('.popover')), 'ArrowDown on the button opens it too')
    await click(900, 500)
    assert.ok(await until(`document.querySelector('.popover') === null`), 'a click outside closes the menu')
    assert.equal(await evaluate(noModal), true, 'and opens nothing')

    // Join with an invite: the dialog, with the field focused and the focus kept inside; Escape, a click outside and the
    // close button close it and give the focus back to "+".
    await evaluate(`${plus}.focus()`)
    await key('ArrowDown')
    assert.ok(await until(visible('.popover')))
    await key('ArrowDown')
    await key('Enter')
    assert.ok(await until(visible('.modal__box')), 'Enter on Join with an invite opens its dialog')
    assert.equal(await evaluate(`document.querySelector('.popover') === null`), true, 'the menu is gone')
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'Join with an invite')
    assert.equal(await evaluate(`document.querySelector('.modal__box').getAttribute('aria-modal')`), 'true')
    assert.equal(await evaluate(`document.querySelector('.modal__box').getAttribute('aria-labelledby') === document.querySelector('.modal__title').id`), true)
    assert.equal(await evaluate(`document.activeElement === document.querySelector('#join-invite')`), true, 'the invite field has the focus')
    assert.deepEqual(await evaluate(`[...document.querySelectorAll('.modal .btn')].map((b) => b.textContent)`), ['Paste', 'Continue'], 'the existing form')
    for (let i = 0; i < 6; i++) {
      await key('Tab')
      assert.equal(await evaluate(inBox), true, `Tab ${i + 1} stays inside`)
    }
    await key('Tab', 8)
    assert.equal(await evaluate(inBox), true, 'Shift+Tab stays inside')
    await key('Escape')
    assert.ok(await until(noModal), 'Escape closes the dialog')
    assert.equal(await evaluate(onPlus), true, 'the focus is back on "+"')
    assert.equal(await evaluate(`document.querySelector('.split') !== null`), true, 'the start screen is still there')

    await evaluate(`${plus}.click()`)
    await evaluate(`document.querySelectorAll('[role=menuitem]')[1].click()`)
    assert.ok(await until(visible('.modal__box')))
    await click(900, 40)
    assert.ok(await until(noModal), 'a click outside closes the dialog')
    assert.equal(await evaluate(onPlus), true, 'the focus is back on "+"')
    await evaluate(`${plus}.click()`)
    await evaluate(`document.querySelectorAll('[role=menuitem]')[1].click()`)
    assert.ok(await until(visible('.modal__box')))
    await evaluate(`document.querySelector('.modal .iconbtn').click()`)
    assert.ok(await until(noModal), 'the close button closes the dialog')
    assert.equal(await evaluate(onPlus), true)

    // The dialog still checks the invite: nothing pasted and a sentence that is not an invite each say so, and the invite
    // continues to the lobby of the call to join.
    await evaluate(`${plus}.click()`)
    await evaluate(`document.querySelectorAll('[role=menuitem]')[1].click()`)
    assert.ok(await until(visible('#join-invite')))
    await evaluate(`document.querySelector('.joinform button[type=submit]').click()`)
    assert.ok(await until(visible('.joinform [role=alert]')), 'an empty invite says so')
    await evaluate(`(() => { const el = document.querySelector('#join-invite'); Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set.call(el, 'koloinv0qx7Zt2mVb9LkR4sWd8YcA1fGh6JnP3uTe5XoBi0MyKvNrCa7DwEzQ2lHs4FgUj9pSx6Ot'); el.dispatchEvent(new Event('input', { bubbles: true })) })()`)
    await evaluate(`document.querySelector('.joinform button[type=submit]').click()`)
    assert.ok(await until(`document.querySelector('.lobby') !== null && document.querySelector('.modal') === null`), 'a good invite goes to the lobby')
    assert.equal(await evaluate(`document.querySelector('.lobby__title').textContent`), 'Join the call')

    // Start a call from the menu: the lobby with the self-view and the devices, and no dialog in the way.
    await open('/?mock=home', home)
    await evaluate(`${plus}.click()`)
    await evaluate(`document.querySelectorAll('[role=menuitem]')[0].click()`)
    assert.ok(await until(visible('.lobby')), 'Start a call opens the lobby')
    assert.equal(await evaluate(`document.querySelector('.lobby__title').textContent`), 'Start a call')
    assert.equal(await evaluate(noModal), true)
    assert.equal(await evaluate(`document.querySelector('.bar__title').textContent`), 'New call')
    // Escape on the lobby's way back: Back returns to the start screen.
    await evaluate(`[...document.querySelectorAll('.lobby .btn')].find((b) => b.textContent === 'Back').click()`)
    assert.ok(await until(home))

    // The first run has no name yet: nothing can start, so the "+" waits.
    await open('/?mock=name', visible('.nameform'))
    assert.equal(await evaluate(`${plus}.disabled`), true)
    assert.equal(await evaluate(`document.querySelector('.dock__name') === null`), true, 'no name, no name in the card')

    // The network is one line and no address: the public network, a network of your own, this computer only.
    for (const [scene, text] of [['home', 'Public network · Ready'], ['own', 'Your own network · Ready'], ['local', 'This computer only · Ready'], ['call-own', 'Your own network · 4 in call'], ['call-local', 'This computer only · 3 in call']]) {
      await open(`/?mock=${scene}`, visible('.dock__net'))
      assert.equal(await evaluate(`document.querySelector('.dock__net').textContent.trim()`), text, scene)
      assert.equal(await evaluate(`/\\d+\\.\\d+\\.\\d+\\.\\d+/.test(document.querySelector('.side').textContent)`), false, `${scene}: no address in the sidebar`)
    }

    // History and Settings open from the card and close with Escape; the focus goes back to their button, and the
    // sidebar waits behind the settings page.
    await open('/?mock=home', home)
    await evaluate(`document.querySelectorAll('.dock__btn')[0].click()`)
    assert.ok(await until(visible('.modal__box')), 'History opens')
    assert.equal(await evaluate(`document.querySelector('.modal__title').textContent`), 'History')
    await key('Escape')
    assert.ok(await until(noModal), 'Escape closes History')
    await evaluate(`document.querySelectorAll('.dock__btn')[1].click()`)
    assert.ok(await until(visible('#settings-name')), 'Settings opens')
    assert.equal(await evaluate(`document.querySelector('.modal--page') !== null && document.querySelector('.side') !== null && document.querySelector('.side').getBoundingClientRect().right <= document.querySelector('.modal--page').getBoundingClientRect().left + 1`), true, 'the page is the main column: the sidebar stays beside it')
    assert.equal(await evaluate(`document.querySelector('.side').hasAttribute('inert')`), true, 'the sidebar waits')
    await key('Escape')
    assert.ok(await until(noModal), 'Escape closes Settings')
    assert.equal(await evaluate(`document.activeElement === document.querySelectorAll('.dock__btn')[1]`), true, 'the focus is back on Settings')
    assert.equal(await evaluate(`document.querySelector('.side').hasAttribute('inert')`), false)

    // The smallest window leaves little for the page: the sidebar is a rail whatever was chosen, with no toggle.
    await send('Emulation.setDeviceMetricsOverride', { width: 640, height: 520, deviceScaleFactor: 1, mobile: false })
    await open('/?mock=call-4', inCall)
    assert.ok(await until(`document.querySelector('.side.is-collapsed') !== null`), 'a rail at 640 px')
    assert.equal(await evaluate(`document.querySelector('[aria-controls="calls-panel"]') === null`), true, 'no toggle on a rail that cannot open')
    assert.ok((await rect('.side')).width <= 70)
    assert.equal(await evaluate(`document.documentElement.scrollWidth <= innerWidth`), true)
    await send('Emulation.setDeviceMetricsOverride', { width: 1000, height: 618, deviceScaleFactor: 1, mobile: false })
    assert.ok(await until(`document.querySelector('.side.is-collapsed') === null`), 'wider, it opens again')
    assert.equal(await evaluate(`document.querySelector('[aria-controls="calls-panel"]') !== null`), true)

    // A narrow window: the sidebar shrinks and the page does not scroll sideways.
    await send('Emulation.setDeviceMetricsOverride', { width: 800, height: 600, deviceScaleFactor: 1, mobile: false })
    await open('/?mock=call-4', inCall)
    assert.ok((await rect('.side')).width <= 250, 'the sidebar is narrow at 800 px')
    assert.equal(await evaluate(`document.documentElement.scrollWidth <= innerWidth`), true, 'nothing scrolls sideways')
    assert.equal(await evaluate(`[...document.querySelectorAll('.controls > *')].every((el) => el.getBoundingClientRect().right <= innerWidth)`), true, 'the controls fit')
  } finally {
    await close()
    await server.close()
  }
})
