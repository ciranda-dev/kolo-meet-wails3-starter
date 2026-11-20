import test from 'node:test'
import assert from 'node:assert/strict'
import { fixCommand, networkCommand, networkCommandParts, networkSources, shellArg } from '../src/command.js'

const KEY = '3b6a27bcceb6a42d62a3a8d02a6f0d73653215771de243a63ac048a18b59da29'
const RELAY = `203.0.113.20:7000@${KEY}`
const base = { networkMode: 'own', entryNodes: '', entryNodesFrom: 'none', connectionRelays: '', connectionRelaysFrom: 'none', firstNode: '', dataDir: '/tmp/kolo-meet-demo/guest' }

test('a word goes into the command as it is, and anything else in single quotes', () => {
  assert.equal(shellArg('/tmp/kolo-meet-demo/host'), '/tmp/kolo-meet-demo/host')
  assert.equal(shellArg(RELAY), RELAY)
  assert.equal(shellArg('/Users/ana/Library/Application Support/kolo-meet'), "'/Users/ana/Library/Application Support/kolo-meet'")
  assert.equal(shellArg("it's"), "'it'\\''s'")
  assert.equal(shellArg(''), "''")
})

test('the command of a window with the address in use asks for -bootstrap with that address', () => {
  const err = { reason: 'first-node-in-use', detail: '127.0.0.1:4000', cause: 'bind: address already in use' }
  assert.equal(fixCommand(base, err), './bin/kolo-meet -data /tmp/kolo-meet-demo/guest -bootstrap 127.0.0.1:4000')
  assert.equal(fixCommand({ ...base, dataDir: '/a b' }, err), "./bin/kolo-meet -data '/a b' -bootstrap 127.0.0.1:4000")
  assert.equal(fixCommand(base, { ...err, reason: 'bootstrap-failed' }), '')
  assert.equal(fixCommand(base, { ...err, detail: '' }), '')
  assert.equal(fixCommand(base, null), '')
})

test('the command of the settings starts the same network again', () => {
  assert.equal(networkCommand({ ...base, firstNode: '127.0.0.1:4000' }), './bin/kolo-meet -data /tmp/kolo-meet-demo/guest -first 127.0.0.1:4000')
  assert.equal(
    networkCommand({ ...base, entryNodes: 'a.example.org:7447, 203.0.113.10:7447', entryNodesFrom: 'env', connectionRelays: `${RELAY},${RELAY.replace('20:', '21:')}`, connectionRelaysFrom: 'flag' }),
    `./bin/kolo-meet -data /tmp/kolo-meet-demo/guest -bootstrap a.example.org:7447,203.0.113.10:7447 -relay ${RELAY} -relay ${RELAY.replace('20:', '21:')}`,
  )
  assert.equal(networkCommand({ ...base, firstNode: '127.0.0.1:4000', entryNodes: '127.0.0.1:4000,127.0.0.1:5000', entryNodesFrom: 'flag' }), './bin/kolo-meet -data /tmp/kolo-meet-demo/guest -first 127.0.0.1:4000 -bootstrap 127.0.0.1:5000')
  // The public network, the default, has no flag; -local is its own flag and shows no entry node.
  const publicNet = { ...base, networkMode: 'public', entryNodesFrom: 'public' }
  assert.equal(networkCommand(publicNet), './bin/kolo-meet -data /tmp/kolo-meet-demo/guest')
  assert.equal(networkCommand({ ...publicNet, connectionRelays: RELAY }), `./bin/kolo-meet -data /tmp/kolo-meet-demo/guest -relay ${RELAY}`)
  assert.equal(networkCommand({ ...base, networkMode: 'local', entryNodes: '127.0.0.1:7451', entryNodesFrom: 'local' }), './bin/kolo-meet -data /tmp/kolo-meet-demo/guest -local')
  assert.equal(networkCommand({ networkMode: undefined, dataDir: '' }), './bin/kolo-meet')
  assert.equal(networkCommand(null), '')
  assert.deepEqual(networkCommandParts({ ...base, dataDir: '/a b', firstNode: '127.0.0.1:4000', connectionRelays: RELAY }), ['./bin/kolo-meet', "-data '/a b'", '-first 127.0.0.1:4000', `-relay ${RELAY}`])
})

test('the network of the settings says what is used and where it came from', () => {
  assert.deepEqual(networkSources({ ...base, firstNode: '127.0.0.1:4000' }), { mode: 'own', nodes: [{ list: ['127.0.0.1:4000'], from: '-first' }], relays: null })
  assert.deepEqual(networkSources({ ...base, entryNodes: 'a:1,b:2', entryNodesFrom: 'flag' }).nodes, [{ list: ['a:1', 'b:2'], from: '-bootstrap' }])
  assert.deepEqual(networkSources({ ...base, entryNodes: 'a:1', entryNodesFrom: 'env' }).nodes, [{ list: ['a:1'], from: 'KOLO_BOOTSTRAP' }])
  assert.deepEqual(networkSources({ ...base, networkMode: 'public', entryNodesFrom: 'public' }), { mode: 'public', nodes: [], relays: null })
  assert.deepEqual(networkSources({ ...base, networkMode: 'public', entryNodes: 'a:1,b:2', entryNodesFrom: 'public' }), { mode: 'public', nodes: [], relays: null })
  assert.deepEqual(networkSources({ dataDir: '' }), { mode: 'public', nodes: [], relays: null })
  assert.deepEqual(networkSources({ ...base, networkMode: 'local', entryNodes: '127.0.0.1:7451', entryNodesFrom: 'local' }), { mode: 'local', nodes: [{ list: ['127.0.0.1:7451'], from: '-local' }], relays: null })
  for (const [from, says] of Object.entries({ flag: '-relay', file: '-relay-file', 'flag-and-file': '-relay, -relay-file' })) {
    assert.deepEqual(networkSources({ ...base, connectionRelays: RELAY, connectionRelaysFrom: from }).relays, { list: ['203.0.113.20:7000'], from: says })
  }
  assert.deepEqual(networkSources({ ...base, connectionRelays: RELAY, connectionRelaysFrom: 'env' }).relays, { list: ['203.0.113.20:7000'], from: 'KOLO_MEET_RELAYS' })
})
