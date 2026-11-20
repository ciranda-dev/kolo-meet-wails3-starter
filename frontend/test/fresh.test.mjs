import test from 'node:test'
import assert from 'node:assert/strict'
import { createGate, newerNetwork } from '../src/fresh.js'

const initial = { state: 'starting', seq: 0 }
const starting = { state: 'starting', seq: 1 }
const ready = { state: 'ready', seq: 2 }

test('the Start snapshot never undoes a newer network event, in either order', () => {
  // The ready event arrives before the answer of Start, which was taken while starting.
  assert.equal([ready, starting].reduce(newerNetwork, initial), ready)
  // The usual order.
  assert.equal([starting, ready].reduce(newerNetwork, initial), ready)
  // A reload while the network is ready: the snapshot is ready, then a relay change sends seq 3.
  const relays = { state: 'ready', seq: 3, relays: [{ state: 'reserved' }] }
  assert.equal([ready, relays].reduce(newerNetwork, initial), relays)
  assert.equal([relays, ready].reduce(newerNetwork, initial), relays)
  // Nothing, or the same state again, changes nothing.
  assert.equal(newerNetwork(ready, null), ready)
  assert.equal(newerNetwork(ready, { ...ready }).seq, 2)
  // A restart of the network keeps counting up, so its states win.
  assert.equal(newerNetwork(ready, { state: 'starting', seq: 4 }).state, 'starting')
})

test('a getter answer that crossed an event is dropped; one that did not is used', () => {
  const gate = createGate()
  // Snapshot first, event after: the snapshot is used, then the event replaces it.
  let mark = gate.mark()
  assert.equal(gate.fresh(mark), true)
  // Event before the answer: the answer is older than or equal to what the event said.
  mark = gate.mark()
  gate.event()
  assert.equal(gate.fresh(mark), false)
  // A new request after the event is fresh again.
  mark = gate.mark()
  assert.equal(gate.fresh(mark), true)
})
