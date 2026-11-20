import test from 'node:test'
import assert from 'node:assert/strict'
import { largestTile } from '../src/tilesize.js'

test('largestTile is the widest and tallest picture in device pixels', () => {
  assert.deepEqual(largestTile([{ width: 660, height: 620 }, { width: 640, height: 630 }], 2), { width: 1320, height: 1260 })
  assert.deepEqual(largestTile([{ width: 200.4, height: 150.2 }], 1.5), { width: 301, height: 225 })
})

test('largestTile without pictures or with a bad ratio', () => {
  assert.deepEqual(largestTile([], 2), { width: 0, height: 0 })
  assert.deepEqual(largestTile([{ width: 100, height: 50 }], 0), { width: 100, height: 50 })
  assert.deepEqual(largestTile([{ width: 100, height: 50 }], Number.NaN), { width: 100, height: 50 })
  assert.deepEqual(largestTile([{ width: 'x', height: null }, undefined], 2), { width: 0, height: 0 })
})
