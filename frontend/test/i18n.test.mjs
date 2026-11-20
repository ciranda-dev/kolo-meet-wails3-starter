import test from 'node:test'
import assert from 'node:assert/strict'
import { locales, languageNames, htmlLang, messages, pickLocale, translate } from '../src/i18n.js'

const places = (text) => (text.match(/\{\w+\}/g) ?? []).sort().join(' ')

test('the three languages have identical key sets, with the same places', () => {
  assert.deepEqual(locales, ['en', 'es', 'pt'])
  const keys = Object.keys(messages.en).sort()
  for (const locale of locales) {
    assert.deepEqual(Object.keys(messages[locale]).sort(), keys, `keys of ${locale}`)
    for (const key of keys) {
      assert.equal(places(messages[locale][key]), places(messages.en[key]), `${locale} ${key}`)
      assert.ok(messages[locale][key].trim() !== '', `${locale} ${key} is empty`)
    }
    assert.ok(languageNames[locale])
    assert.ok(htmlLang[locale])
  }
})

test('the copy has no em dash and no exclamation mark', () => {
  for (const locale of locales) {
    for (const [key, text] of Object.entries(messages[locale])) {
      assert.ok(!/[\u2014!\u00a1]/.test(text), `${locale} ${key}: ${text}`)
    }
  }
})

test('the copy makes no claim beyond the words of the contract', () => {
  // The only sentences about protection are the security keys; nothing else may promise anything.
  const claims = /\b(secure|securely|private|privately|safe|nobody can|no one can|seguro|segura|privad[oa]|ningu[eé]m pode|nadie puede)\b/i
  for (const locale of locales) {
    for (const [key, text] of Object.entries(messages[locale])) {
      // The error sentences are the contract's own words, pinned in errors.test.mjs.
      if (key.startsWith('error.')) {
        continue
      }
      assert.ok(!claims.test(text), `${locale} ${key}: ${text}`)
    }
  }
  for (const locale of locales) {
    const encrypted = Object.entries(messages[locale]).filter(([, text]) => /encrypt|cifrad|criptograf/i.test(text)).map(([key]) => key)
    // security.detail.relays is the sentence of security.go about connection relays, pinned in security.test.mjs.
    assert.deepEqual(encrypted, ['security.header.guest', 'security.header.host', 'security.detail.relays'], locale)
  }
})

test('no sentence sends the person to Settings for the network', () => {
  // The network and the connection relays are set with flags; Settings only shows them.
  for (const locale of locales) {
    for (const [key, text] of Object.entries(messages[locale])) {
      if (key.startsWith('error.') || key.startsWith('network.')) {
        assert.ok(!/\b(in|en|em) (Settings|Ajustes|Configurações)\b/.test(text), `${locale} ${key}: ${text}`)
      }
    }
  }
  assert.equal(messages.en['network.openSettings'], undefined)
})

test('there is no chat, message or file copy', () => {
  for (const key of Object.keys(messages.en)) {
    assert.ok(!/^(chat|message|file|share)\./.test(key), key)
  }
})

test('pickLocale prefers the saved language, then the system, then English', () => {
  assert.equal(pickLocale('pt', ['es-ES']), 'pt')
  assert.equal(pickLocale(undefined, ['fr-FR', 'es-MX']), 'es')
  assert.equal(pickLocale('system', ['pt-BR']), 'pt')
  assert.equal(pickLocale(undefined, ['fr-FR']), 'en')
  assert.equal(pickLocale(undefined), 'en')
})

test('translate fills places and picks the form by n', () => {
  assert.equal(translate('en', 'history.people', { n: 1 }), '1 person')
  assert.equal(translate('en', 'history.people', { n: 3 }), '3 people')
  assert.equal(translate('es', 'history.people', { n: 2 }), '2 personas')
  assert.equal(translate('pt', 'badge.call', { n: 4 }), '4 na chamada')
  assert.equal(translate('en', 'no.such.key'), 'no.such.key')
  assert.equal(translate('xx', 'call.leave'), 'Leave')
  assert.equal(translate('en', 'settings.version', {}), 'Kolo Meet {v}')
})
