import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// Every method the window calls exists in the generated bindings (when they are here) and in the mock.
const src = fileURLToPath(new URL('../src/', import.meta.url))
const bindings = fileURLToPath(new URL('../bindings/github.com/ciranda-dev/kolo-meet-wails3-starter/', import.meta.url))
const mock = readFileSync(fileURLToPath(new URL('../dev/mock.ts', import.meta.url)), 'utf8')
const called = new Set()
for (const name of readdirSync(src).filter((n) => /\.jsx?$/.test(n))) {
  for (const m of readFileSync(`${src}${name}`, 'utf8').matchAll(/\bcall\('(\w+)'/g)) {
    called.add(m[1])
  }
}

test('the window calls the microphone methods, and none that changes the network', () => {
  for (const name of ['MicrophoneAccess', 'RequestMicrophone', 'CancelMicrophoneRequest']) {
    assert.ok(called.has(name), name)
  }
  // The network (public, your own or this computer) and the connection relays come from the flags: the window only shows them.
  for (const name of ['SaveSettings', 'SaveRelays']) {
    assert.ok(!called.has(name), name)
  }
})

test('every method the window calls is in the mock', () => {
  for (const name of called) {
    assert.ok(new RegExp(`export async function ${name}\\(`).test(mock), name)
  }
})

test('every method the window calls is in the generated bindings', { skip: !existsSync(`${bindings}meetservice.js`) && 'bindings not generated' }, () => {
  const service = readFileSync(`${bindings}meetservice.js`, 'utf8')
  for (const name of called) {
    assert.ok(new RegExp(`export function ${name}\\(`).test(service), name)
  }
  const models = readFileSync(`${bindings}models.js`, 'utf8')
  for (const field of ['connectionRelays', 'connectionRelaysFrom', 'relays', 'warning', 'relayLink', 'relayVia', 'connectionRelay', 'link']) {
    assert.ok(new RegExp(`"${field}"`).test(models), field)
  }
})
