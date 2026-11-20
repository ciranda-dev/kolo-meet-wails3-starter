import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { locales, messages, translate } from '../src/i18n.js'
import { errorSentence, meetError } from '../src/errors.js'

// The reasons table of errors.go is the source: the window must carry each reason with the same
// English sentence and a sentence in every other language, so a new or reworded reason fails here.
const goErrors = new URL('../../errors.go', import.meta.url)

/** The reasons map of errors.go as reason -> { kind, text }, with each kind constant turned into its value. */
function goReasons() {
  const text = readFileSync(goErrors, 'utf8')
  const kinds = Object.fromEntries([...text.matchAll(/^\t(kind\w+)\s*=\s*"([\w-]+)"$/gm)].map((m) => [m[1], m[2]]))
  const start = text.indexOf('var reasons = map[string]')
  const block = text.slice(start, text.indexOf('\n}', start))
  return Object.fromEntries([...block.matchAll(/^\t"([\w-]+)":\s*\{(\w+), "((?:[^"\\]|\\.)*)"\},?$/gm)].map((m) => [m[1], { kind: kinds[m[2]], text: JSON.parse(`"${m[3]}"`) }]))
}

test('every reason of errors.go has its sentence in every language, and nothing more', () => {
  const listed = goReasons()
  const reasons = Object.keys(listed)
  assert.ok(reasons.length >= 60)
  for (const reason of ['call-full', 'call-still-ending', 'microphone-silent', 'microphone-restricted', 'connection-relay-not-allowed', 'connection-relay-full', 'connection-relay-call-denied', 'connection-relay-wrong-key', 'connection-relay-bad-key', 'connection-relay-unreachable', 'connection-relay-none-shared', 'host-unreachable', 'host-unreachable-relays', 'relay-no-common', 'first-node-in-use']) {
    assert.ok(reasons.includes(reason), reason)
  }
  for (const [reason, { kind, text }] of Object.entries(listed)) {
    assert.ok(kind, `${reason} has a kind`)
    assert.equal(messages.en[`error.${reason}`], text, `en error.${reason}`)
    for (const locale of locales) {
      const sentence = messages[locale][`error.${reason}`]
      assert.ok(typeof sentence === 'string' && sentence.length > 0, `${locale} error.${reason}`)
      assert.ok(!/sorry|lo sentimos|perd[oó]n|desculp/i.test(sentence), `${locale} error.${reason} apologizes`)
    }
  }
  for (const locale of locales) {
    const extra = Object.keys(messages[locale]).filter((k) => k.startsWith('error.') && !reasons.includes(k.slice(6)))
    assert.deepEqual(extra, [], locale)
  }
})

const t = (locale) => (key, vars) => translate(locale, key, vars)
const relay = { kind: 'network', reason: 'relay-unreachable', detail: '', cause: 'timeout', message: "Could not connect to the call's relay: timeout." }

test('an error is translated by its reason, with its detail and cause', () => {
  assert.equal(errorSentence(t('en'), 'en', relay), relay.message)
  assert.equal(errorSentence(t('es'), 'es', relay), 'No se pudo conectar con el relé de la llamada: timeout.')
  assert.equal(errorSentence(t('pt'), 'pt', relay), 'Não foi possível conectar ao relé da chamada: timeout.')
  const long = { kind: 'name', reason: 'name-too-long', detail: '48', cause: '', message: 'x' }
  assert.equal(errorSentence(t('en'), 'en', long), 'The name is too long. Use at most 48 characters.')
})

test('a rejected call carries the error as its cause, as an object or as JSON', () => {
  const thrown = Object.assign(new Error(relay.message), { cause: relay })
  assert.equal(meetError(thrown), relay)
  assert.equal(errorSentence(t('es'), 'es', thrown), 'No se pudo conectar con el relé de la llamada: timeout.')
  const asJson = Object.assign(new Error(relay.message), { cause: JSON.stringify(relay) })
  assert.equal(meetError(asJson).reason, 'relay-unreachable')
  assert.equal(meetError(Object.assign(new Error('x'), { cause: '{broken' })), null)
})

test('an unknown reason keeps the English sentence of the service', () => {
  const unknown = { kind: 'internal', reason: 'brand-new', detail: '', cause: '', message: 'A new sentence.' }
  assert.equal(errorSentence(t('pt'), 'pt', unknown), 'A new sentence.')
  assert.equal(errorSentence(t('pt'), 'pt', new Error('plain')), 'plain')
  assert.equal(errorSentence(t('pt'), 'pt', null, 'fallback'), 'fallback')
  assert.equal(errorSentence(t('es'), 'es', { key: 'join.needInvite' }), 'Pega primero la invitación.')
})
