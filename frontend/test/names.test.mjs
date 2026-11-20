import test from 'node:test'
import assert from 'node:assert/strict'
import { initial, safeText } from '../src/format.js'
import { translate } from '../src/i18n.js'
import { errorSentence } from '../src/errors.js'
import { securityDetail, securityHeader, MODE } from '../src/security.js'

const hidden = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{Co}\p{Cs}]/u

test('a right-to-left override cannot turn one name into another', () => {
  const shown = safeText('Marta \u202eariervilO')
  assert.equal(shown, 'Marta [U+202E]ariervilO')
  assert.ok(!hidden.test(shown))
})

test('every bidi control is shown as its code', () => {
  for (const code of [0x061c, 0x200e, 0x200f, 0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069]) {
    const shown = safeText(`a${String.fromCodePoint(code)}b`)
    assert.equal(shown, `a[U+${code.toString(16).toUpperCase().padStart(4, '0')}]b`)
    assert.ok(!hidden.test(shown))
  }
})

test('zero-width, control and separator characters are shown too', () => {
  assert.equal(safeText('a\u200bb\u200cc\u200dd\ufeffe\u2060f'), 'a[U+200B]b[U+200C]c[U+200D]d[U+FEFF]e[U+2060]f')
  assert.equal(safeText('line\nbreak\ttab\u0000nul\u007fdel\u0085nel'), 'line[U+000A]break[U+0009]tab[U+0000]nul[U+007F]del[U+0085]nel')
  assert.equal(safeText('a\u2028b\u2029c\u00add'), 'a[U+2028]b[U+2029]c[U+00AD]d')
  assert.equal(safeText('tag\u{e0041}x'), 'tag[U+E0041]x')
  assert.equal(safeText('lone\ud800x'), 'lone[U+D800]x')
})

test('ordinary names in any script stay as they are', () => {
  for (const name of ['Ana Ribeiro', 'Tomás Herrera', 'Léa Fontaine', 'שלום', 'عبد الرحمن', '田中 由紀', 'a b  c', 'emoji \u{1F600}', '']) {
    assert.equal(safeText(name), name)
  }
  assert.equal(safeText(undefined), '')
  assert.equal(safeText(null), '')
})

test('a name that looks like markup stays text', () => {
  const name = '<img src=x onerror=alert(1)>'
  assert.equal(safeText(name), name)
  assert.equal(safeText('<b>\u202ex</b>'), '<b>[U+202E]x</b>')
})

test('the initial of an avatar is a visible character', () => {
  assert.equal(initial('ana'), 'A')
  assert.equal(initial('  Élise'), 'É')
  assert.equal(initial('田中'), '田')
  assert.equal(initial('\u202eevil'), '[')
  assert.equal(initial(''), '?')
  assert.equal(initial(undefined), '?')
  assert.ok(!hidden.test(initial('\u200b\u200d')))
})

test('an error sentence escapes the detail and the cause it carries', () => {
  const t = (key, vars) => translate('es', key, vars)
  const err = { kind: 'config', reason: 'data-dir-open', detail: '/tmp/\u202ex', cause: 'bad\u200bword', message: 'x' }
  const text = errorSentence(t, 'es', err)
  assert.ok(text.includes('/tmp/[U+202E]x'))
  assert.ok(!hidden.test(text))
  const unknown = { kind: 'k', reason: 'not-known', detail: '', cause: '', message: 'Name \u202eevil' }
  assert.equal(errorSentence(t, 'es', unknown), 'Name [U+202E]evil')
  assert.equal(errorSentence(t, 'es', new Error('plain \u2066x')), 'plain [U+2066]x')
})

test('the name of who runs the relay is escaped in the security text', () => {
  for (const locale of ['en', 'es', 'pt']) {
    const t = (key, vars) => translate(locale, key, vars)
    const security = { mode: MODE, header: 'x', detail: 'x', operator: 'Marta \u202eariervilO', operatorIsYou: false, participants: 3, waiting: 0 }
    const text = [securityHeader(t, security), ...securityDetail(t, security)].join(' ')
    assert.ok(text.includes('Marta [U+202E]ariervilO'), locale)
    assert.ok(!hidden.test(text), locale)
  }
  const t = (key, vars) => translate('en', key, vars)
  const later = { mode: 'later', header: 'A \u202eline', detail: 'More \u200bwords', operator: '', operatorIsYou: false, participants: 2, waiting: 0 }
  assert.ok(!hidden.test(securityHeader(t, later) + securityDetail(t, later).join('')))
})
