import test from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { locales, translate } from '../src/i18n.js'
import { MODE, complete, securityDetail, securityHeader } from '../src/security.js'

// The exact English of the contract ("Security"). The window may say this and nothing more.
const COMMON = "The relay cannot see audio, video or names, not even how long a name is. Anyone in the call could, with the relay's help, make audio, video or names that seem to come from someone else. The relay sees who is in the call, who watches whom, when each person sends and how much, when microphones and cameras are off, and the room ID; it can drop or delay media. Anyone in the call can read everyone's media. There is no protection for the current key period if a device is compromised, and no recovery after a compromise."
const RUN_GUEST = ' Marta runs the relay, so Marta could make audio, video or names that seem to come from someone else.'
const RUN_HOST = ' You run the relay, so you could make audio, video or names that seem to come from someone else.'
const WAITING = ' Nothing from a person who is waiting for keys is played.'
const guest = { mode: MODE, operator: 'Marta', operatorIsYou: false, participants: 3, waiting: 0, complete: true, header: '3 in call, end-to-end encrypted, through relay run by Marta', detail: COMMON + RUN_GUEST }
const host = { mode: MODE, operator: 'Ana', operatorIsYou: true, participants: 2, waiting: 0, complete: true, header: '2 in call, end-to-end encrypted, through relay run by you', detail: COMMON + RUN_HOST }
const guestWaiting = { ...guest, participants: 4, waiting: 2, complete: false, header: '4 in call, 2 waiting for keys, through relay run by Marta', detail: COMMON + RUN_GUEST + WAITING }
const hostWaiting = { ...host, participants: 3, waiting: 1, complete: false, header: '3 in call, 1 waiting for keys, through relay run by you', detail: COMMON + RUN_HOST + WAITING }
const t = (locale) => (key, vars) => translate(locale, key, vars)
const goSecurity = fileURLToPath(new URL('../../security.go', import.meta.url))
const END_TO_END = /end-to-end|extremo a extremo|ponta a ponta|encrypt|cifrad|criptograf/i

test('in English the window says exactly what the service says', () => {
  for (const security of [guest, host, guestWaiting, hostWaiting]) {
    assert.equal(securityHeader(t('en'), security), security.header)
    assert.equal(securityDetail(t('en'), security).join(' '), security.detail)
  }
})

test('the English sentences are the ones in security.go', { skip: !existsSync(goSecurity) && 'security.go is not here' }, () => {
  const source = readFileSync(goSecurity, 'utf8').replace(/"\s*\+\s*"/g, '')
  for (const sentence of securityDetail(t('en'), hostWaiting)) {
    assert.ok(source.includes(sentence), sentence)
  }
  for (const line of ['%d in call, end-to-end encrypted, through relay run by %s', '%d in call, %d waiting for keys, through relay run by %s', '%s runs the relay, so %s could make audio, video or names that seem to come from someone else.']) {
    assert.ok(source.includes(line), line)
  }
})

test('while someone waits for keys no language says end-to-end', () => {
  for (const locale of locales) {
    for (const security of [guestWaiting, hostWaiting, { ...guest, complete: false }, { ...guest, waiting: 1 }, { ...guest, complete: undefined }]) {
      const header = securityHeader(t(locale), security)
      assert.ok(!END_TO_END.test(header), `${locale}: ${header}`)
      assert.ok(header.startsWith(`${security.participants} `), locale)
    }
    assert.ok(END_TO_END.test(securityHeader(t(locale), guest)), locale)
    assert.ok(securityHeader(t(locale), guestWaiting).includes(' 2 '), `${locale}: the number waiting`)
    assert.ok(securityHeader(t(locale), guestWaiting).endsWith('Marta'), locale)
  }
  assert.equal(complete(guest), true)
  assert.equal(complete(guestWaiting), false)
  assert.equal(complete({ ...guest, complete: undefined }), false)
  assert.equal(complete(null), false)
})

test('every language keeps the whole text, in order, with who runs the relay', () => {
  for (const locale of locales) {
    const detail = securityDetail(t(locale), guest)
    assert.equal(detail.length, 6, locale)
    assert.equal(securityDetail(t(locale), guestWaiting).length, 7, locale)
    assert.ok(detail[5].split('Marta').length === 3, `${locale}: the operator is named twice`)
    assert.ok(securityHeader(t(locale), guest).startsWith('3 '), locale)
    assert.ok(securityHeader(t(locale), guest).endsWith('Marta'), locale)
    assert.ok(/ID/.test(detail[2]), `${locale}: the room ID`)
    assert.ok(!securityDetail(t(locale), host).join(' ').includes('Ana'), locale)
  }
  // The contract's own Spanish and Portuguese for the new sentences.
  assert.equal(securityDetail(t('es'), guestWaiting)[0], 'El relé no puede ver audio, vídeo ni nombres, ni siquiera cuánto mide un nombre.')
  assert.equal(securityDetail(t('es'), guestWaiting)[6], 'No se reproduce nada de una persona que espera las claves.')
  assert.equal(securityHeader(t('es'), guestWaiting), '4 en la llamada, 2 esperando claves, por un relé de Marta')
  assert.equal(securityDetail(t('pt'), guestWaiting)[1], 'Qualquer pessoa na chamada poderia, com a ajuda do relé, fazer áudio, vídeo ou nomes parecerem vir de outra pessoa.')
  assert.equal(securityDetail(t('pt'), guestWaiting)[5], 'Marta executa o relé, então Marta poderia fazer áudio, vídeo ou nomes parecerem vir de outra pessoa.')
  assert.equal(securityHeader(t('pt'), guestWaiting), '4 na chamada, 2 esperando chaves, por um relé de Marta')
})

const RELAYS = ' Each connection relay in your settings sees your key, your address and when you are in a call, and, for the connections it carries, who connects to whom, when and how much. It cannot read them: they are encrypted end to end between the two apps.'

test('with connection relays listed the text ends with what each relay sees, as security.go says', () => {
  const guestRelays = { ...guest, detail: COMMON + RUN_GUEST + RELAYS }
  const waitingRelays = { ...hostWaiting, detail: COMMON + RUN_HOST + WAITING + RELAYS }
  for (const security of [guestRelays, waitingRelays]) {
    assert.equal(securityDetail(t('en'), security).join(' '), security.detail)
    // The header does not change with the relays.
    assert.equal(securityHeader(t('en'), security), security.header)
  }
  for (const locale of locales) {
    assert.equal(securityDetail(t(locale), guestRelays).length, 7, locale)
    assert.equal(securityDetail(t(locale), waitingRelays).length, 8, locale)
    assert.equal(securityDetail(t(locale), guest).length, 6, `${locale}: no relay sentence without relays`)
  }
  if (existsSync(goSecurity)) {
    const source = readFileSync(goSecurity, 'utf8').replace(/"\s*\+\s*"/g, '')
    assert.ok(source.includes(RELAYS), 'the sentence is the one of security.go')
  }
})

test('a mode this window does not know shows the words of the service as they came', () => {
  const later = { ...guest, mode: 'some-later-mode', header: '3 in call, another line', detail: 'Another text.' }
  assert.equal(securityHeader(t('pt'), later), '3 in call, another line')
  assert.deepEqual(securityDetail(t('pt'), later), ['Another text.'])
})

test('a guest call without the name of who runs the relay shows the words of the service', () => {
  const unnamed = { ...guest, operator: '', header: '3 in call, end-to-end encrypted, through relay run by ', detail: 'As it came.' }
  assert.equal(securityHeader(t('es'), unnamed), unnamed.header)
  assert.deepEqual(securityDetail(t('es'), unnamed), ['As it came.'])
})

test('before the call is active there is nothing to say', () => {
  const empty = { mode: '', header: '', detail: '', operator: '', operatorIsYou: false, participants: 0, waiting: 0, complete: false }
  assert.equal(securityHeader(t('en'), empty), '')
  assert.deepEqual(securityDetail(t('en'), empty), [])
  assert.equal(securityHeader(t('en'), null), '')
})
