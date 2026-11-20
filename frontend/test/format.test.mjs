import test from 'node:test'
import assert from 'node:assert/strict'
import { cleanInvite, formatDuration, formatWhen, frameSrc, looksLikeInvite } from '../src/format.js'
import { detectOS } from '../src/platform.js'

test('formatDuration is a clock', () => {
  assert.equal(formatDuration(0), '0:00')
  assert.equal(formatDuration(7300), '0:07')
  assert.equal(formatDuration(724000), '12:04')
  assert.equal(formatDuration(3753000), '1:02:33')
  assert.equal(formatDuration(-5), '0:00')
  assert.equal(formatDuration(undefined), '0:00')
})

test('formatWhen shows the time today and the day otherwise', () => {
  const now = new Date(2026, 10, 16, 15, 0)
  assert.match(formatWhen(new Date(2026, 10, 16, 9, 5), 'en', now), /9:05/)
  assert.match(formatWhen(new Date(2026, 10, 9, 9, 5), 'en', now), /Nov 9/)
  assert.equal(formatWhen('nonsense', 'en', now), '')
})

test('an invite ignores spaces and line breaks, and starts with koloinv0', () => {
  assert.equal(cleanInvite(' koloinv0ab\n cd\t'), 'koloinv0abcd')
  assert.ok(looksLikeInvite('  kolo\ninv0abc'))
  assert.ok(!looksLikeInvite('hello'))
  assert.ok(!looksLikeInvite(''))
  assert.ok(!looksLikeInvite(undefined))
})

test('a picture becomes a data URL only when it is base64', () => {
  assert.equal(frameSrc('/9j/4AAQ'), 'data:image/jpeg;base64,/9j/4AAQ')
  assert.equal(frameSrc('AAAA=='), 'data:image/jpeg;base64,AAAA==')
  assert.equal(frameSrc(''), '')
  assert.equal(frameSrc(undefined), '')
  assert.equal(frameSrc('AAAA" onerror="x'), '')
  assert.equal(frameSrc('javascript:alert(1)'), '')
  assert.equal(frameSrc([1, 2, 3]), '')
})

test('detectOS picks the words of the permission fix', () => {
  assert.equal(detectOS({ platform: 'MacIntel' }), 'mac')
  assert.equal(detectOS({ userAgentData: { platform: 'Windows' } }), 'windows')
  assert.equal(detectOS({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0)' }), 'windows')
  assert.equal(detectOS({ platform: 'Linux x86_64' }), 'linux')
  assert.equal(detectOS({}), 'linux')
  assert.equal(detectOS(), 'linux')
})
