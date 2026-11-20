import test from 'node:test'
import assert from 'node:assert/strict'
import { translate } from '../src/i18n.js'
import { relayList, relayProblems, relaySummary, splitRelay } from '../src/relays.js'

const KEY_A = 'a'.repeat(64)
const KEY_B = 'b'.repeat(64)
const A = `203.0.113.20:7000@${KEY_A}`
const B = `relay.example.org:7001@${KEY_B}`
const t = (locale) => (key, vars) => translate(locale, key, vars)
const unreachable = { kind: 'network', reason: 'connection-relay-unreachable', detail: '203.0.113.20:7000', cause: 'timeout', message: 'x' }

test('the list of the service becomes entries in the same order', () => {
  assert.deepEqual(relayList(`${A}, ${B}`), [A, B])
  assert.deepEqual(relayList(`${B}\n${A}`), [B, A])
  assert.deepEqual(relayList(''), [])
  assert.deepEqual(relayList(undefined), [])
})

test('an entry splits at its last @', () => {
  assert.deepEqual(splitRelay(A), { addr: '203.0.113.20:7000', key: KEY_A })
  assert.deepEqual(splitRelay('a@b@c'), { addr: 'a@b', key: 'c' })
  assert.deepEqual(splitRelay('nothing'), { addr: 'nothing', key: '' })
})

test('the summary counts the relays, and the ready ones during a call', () => {
  assert.equal(relaySummary(t('en'), []), '')
  assert.equal(relaySummary(t('en'), null), '')
  const idle = [{ addr: 'x', key: KEY_A, state: 'idle', error: null }]
  assert.equal(relaySummary(t('en'), idle), '1 connection relay listed, used only during a call.')
  assert.equal(relaySummary(t('en'), [...idle, { ...idle[0], key: KEY_B }]), '2 connection relays listed, used only during a call.')
  const call = [{ addr: 'x', key: KEY_A, state: 'reserved', error: null }, { addr: 'y', key: KEY_B, state: 'not-reserved', error: unreachable }]
  assert.equal(relaySummary(t('en'), call), 'Connection relays: 1 of 2 ready.')
  assert.equal(relaySummary(t('es'), call), 'Relés de conexión: 1 de 2 listos.')
  assert.equal(relaySummary(t('pt'), call), 'Relés de conexão: 1 de 2 prontos.')
  assert.deepEqual(relayProblems(call), [call[1]])
  assert.deepEqual(relayProblems(idle), [])
  assert.deepEqual(relayProblems(undefined), [])
})
