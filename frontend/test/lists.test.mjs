import test from 'node:test'
import assert from 'node:assert/strict'
import { withLists } from '../src/lists.js'
import { translate } from '../src/i18n.js'

test('a list Go never made becomes a list, and the count of a past call stays a count', () => {
  const [item] = withLists('History', [{ id: 'h1', participants: 4, seconds: 2764 }])
  assert.equal(item.participants, 4, 'the history counts the people of a call')
  assert.equal(translate('en', 'history.people', { n: item.participants }), '4 people')
  assert.deepEqual(withLists('Call', { participants: null, relays: undefined }).participants, [])
  assert.deepEqual(withLists('meet:network', { relays: null }).relays, [])
  assert.deepEqual(withLists('History', null), [])
  assert.deepEqual(withLists('Devices', { cameras: null, microphones: null, speakers: null }), { cameras: [], microphones: [], speakers: [] })
})
