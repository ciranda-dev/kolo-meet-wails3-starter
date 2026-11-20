import test from 'node:test'
import assert from 'node:assert/strict'
import { callReducer, noCall } from '../src/calls.js'
import { createMediaStore } from '../src/mediastore.js'

const run = (...actions) => actions.reduce(callReducer, noCall)
const ev = (id, seq, state, more = {}) => ({ type: 'call', call: { id, seq, state, ...more } })
const dismiss = { type: 'dismiss' }

test('events of one call apply in order of seq, whatever order they arrive in', () => {
  const s = run(ev('a', 1, 'joining'), ev('a', 3, 'active'), ev('a', 2, 'connecting'), ev('a', 3, 'connecting'))
  assert.equal(s.call.state, 'active')
  assert.equal(s.call.seq, 3)
  assert.equal(run(ev('a', 5, 'active'), ev('a', 6, 'active', { participants: [1, 2] })).call.seq, 6)
})

test('a stale active event never wins over ended', () => {
  const ended = run(ev('a', 4, 'active'), ev('a', 9, 'ended'))
  assert.equal(callReducer(ended, ev('a', 8, 'active')).call.state, 'ended')
  // Not even with a higher seq: nothing comes after the end of a call.
  assert.equal(callReducer(ended, ev('a', 10, 'active')).call.state, 'ended')
  assert.equal(callReducer(ended, ev('a', 9, 'ended')), ended)
})

test('after the end screen is closed, a late event does not bring the call back', () => {
  const s = run(ev('a', 4, 'active'), ev('a', 9, 'ended'), dismiss)
  assert.equal(s.call, null)
  assert.equal(callReducer(s, ev('a', 8, 'active')).call, null)
  assert.equal(callReducer(s, ev('a', 12, 'active')).call, null)
  assert.equal(callReducer(s, ev('a', 9, 'ended')).call, null)
})

test('a late answer of CreateCall or JoinCall does not replace newer events, nor revive a call', () => {
  // The events got ahead of the answer of the call that started it.
  const ahead = run(ev('a', 3, 'active'), ev('a', 1, 'creating'))
  assert.equal(ahead.call.state, 'active')
  // The person cancelled and the copy was put away before the answer arrived.
  const cancelled = run(ev('a', 2, 'joining'), dismiss, ev('a', 1, 'joining'))
  assert.equal(cancelled.call, null)
  // The call ended and the answer arrived after that.
  const over = run(ev('a', 2, 'joining'), ev('a', 5, 'ended'), ev('a', 1, 'joining'))
  assert.equal(over.call.state, 'ended')
})

test('after Leave, what is left of the old call does not show while a new call runs', () => {
  const next = run(ev('a', 4, 'active'), ev('a', 9, 'ended'), dismiss, ev('b', 1, 'creating'))
  assert.equal(next.call.id, 'b')
  assert.equal(callReducer(next, ev('a', 7, 'active')).call.id, 'b')
  assert.equal(callReducer(next, ev('a', 9, 'ended')).call.id, 'b')
  // An old call whose end this window never saw is still an old call.
  const missed = run(ev('a', 4, 'active'), dismiss, ev('b', 2, 'active'), ev('a', 5, 'active'))
  assert.equal(missed.call.id, 'b')
  const replaced = run(ev('a', 4, 'active'), ev('b', 1, 'joining'), ev('a', 5, 'active'))
  assert.equal(replaced.call.id, 'b')
})

test('a new call is taken, also right after an ended one', () => {
  const s = run(ev('a', 9, 'ended'), ev('b', 1, 'creating'))
  assert.equal(s.call.id, 'b')
  assert.equal(run(ev('a', 1, 'creating')).call.id, 'a')
  assert.equal(callReducer(s, { type: 'call', call: null }), s)
  assert.equal(callReducer(s, { type: 'call', call: { seq: 3, state: 'active' } }), s)
  assert.equal(callReducer(noCall, dismiss), noCall)
  // A call without seq (an older service) is still taken as the newer copy.
  assert.equal(run(ev('a', 2, 'joining'), { type: 'call', call: { id: 'a', state: 'active' } }).call.state, 'active')
})

test('a new invite goes only to the running call that asked for it', () => {
  const active = run(ev('a', 4, 'active', { invite: 'one' }))
  assert.equal(callReducer(active, { type: 'invite', id: 'a', invite: 'two' }).call.invite, 'two')
  assert.equal(callReducer(active, { type: 'invite', id: 'b', invite: 'two' }).call.invite, 'one')
  assert.equal(callReducer(active, { type: 'invite', id: 'a', invite: '' }).call.invite, 'one')
  const ended = callReducer(active, ev('a', 5, 'ended', { invite: '' }))
  assert.equal(callReducer(ended, { type: 'invite', id: 'a', invite: 'late' }).call.invite, '')
  assert.equal(callReducer(callReducer(ended, dismiss), { type: 'invite', id: 'a', invite: 'late' }).call, null)
})

test('pictures and levels are kept only for the people of the call', () => {
  const store = createMediaStore()
  store.keep(['p1', 'p2'])
  store.frame('p1', 'one')
  store.frame('p2', 'two')
  store.frame('self', 'me')
  store.level('p2', { level: 0.5, speaking: true })
  assert.equal(store.picture('p1'), 'one')
  // Someone who is not in the call leaves no state at all.
  assert.deepEqual(store.frame('stranger', 'x'), [])
  assert.deepEqual(store.level('stranger', { level: 1, speaking: true }), [])
  assert.equal(store.picture('stranger'), undefined)
  assert.equal(store.levelOf('stranger'), undefined)
  // p2 leaves: its picture and its level go with it.
  store.keep(['p1'])
  assert.equal(store.picture('p2'), undefined)
  assert.equal(store.levelOf('p2'), undefined)
  assert.equal(store.picture('p1'), 'one')
  assert.equal(store.picture('self'), 'me')
  // The call ends: only the self-view of the lobby may stay.
  store.keep([])
  assert.equal(store.picture('p1'), undefined)
  assert.equal(store.picture('self'), 'me')
  assert.equal(store.size(), 1)
})

test('a tile that goes away leaves no entry behind', () => {
  const store = createMediaStore()
  store.keep(['p1'])
  const img = {}
  const offImage = store.show('p1', img)
  const offLevel = store.watch('p1', () => {})
  assert.deepEqual(store.frame('p1', 'one'), [img])
  offImage()
  offLevel()
  assert.deepEqual(store.frame('p1', 'two'), [])
  assert.deepEqual(store.drop('p1'), [])
  assert.equal(store.size(), 0)
  // Many people coming and going do not make the store grow.
  for (let i = 0; i < 1000; i++) {
    store.keep([`x${i}`])
    store.frame(`x${i}`, 'pic')
    store.level(`x${i}`, { level: 0, speaking: false })
  }
  assert.equal(store.size(), 2)
})
