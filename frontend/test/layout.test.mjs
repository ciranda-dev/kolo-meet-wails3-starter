import test from 'node:test'
import assert from 'node:assert/strict'
import { gridLayout } from '../src/layout.js'

// The room the grid has: the default window, the smallest window, and a tall narrow one.
const BOXES = [[976, 420], [624, 330], [624, 700], [1400, 800]]

test('1 to 8 tiles (and the ninth place) always fit their box', () => {
  for (const [width, height] of BOXES) {
    for (let n = 1; n <= 9; n++) {
      const g = gridLayout(n, width, height, 8)
      assert.ok(g.cols * g.rows >= n, `${n} in ${width}x${height}: places`)
      assert.ok(g.cols * g.width + (g.cols - 1) * 8 <= width, `${n} in ${width}x${height}: width`)
      assert.ok(g.rows * g.height + (g.rows - 1) * 8 <= height, `${n} in ${width}x${height}: height`)
      const aspect = g.width / g.height
      assert.ok(aspect >= 0.68 && aspect <= 2.42, `${n} in ${width}x${height}: aspect ${aspect}`)
    }
  }
})

test('tiles stay large enough to read a name at the smallest window', () => {
  for (let n = 1; n <= 8; n++) {
    const g = gridLayout(n, 624, 330, 8)
    assert.ok(g.width >= 140 && g.height >= 100, `${n}: ${g.width}x${g.height}`)
  }
})

// The room under the header, the protection line and the bar, at the windows the owner uses.
test('the first row of tiles spans at least 90% of a large window, for 1 to 8 people', () => {
  for (const [width, height] of [[1424, 730], [1984, 1070], [2544, 1270]]) {
    for (let n = 1; n <= 9; n++) {
      const g = gridLayout(n, width, height, 8)
      const row = g.cols * g.width + (g.cols - 1) * 8
      assert.ok(g.last >= 1 && g.last <= g.cols && g.lastWidth >= g.width, `${n}: the last row`)
      assert.ok(g.last * g.lastWidth + (g.last - 1) * 8 <= width, `${n}: the last row fits`)
      assert.ok(row >= 0.9 * width, `${n} in ${width}x${height}: the row is ${Math.round((100 * row) / width)}% wide`)
      const column = g.rows * g.height + (g.rows - 1) * 8
      assert.ok(column >= 0.9 * height, `${n} in ${width}x${height}: the rows are ${Math.round((100 * column) / height)}% high`)
    }
  }
})

test('the shape follows the box', () => {
  assert.deepEqual([gridLayout(1, 976, 420).cols, gridLayout(2, 976, 420).cols, gridLayout(4, 976, 420).cols], [1, 2, 2])
  assert.equal(gridLayout(2, 400, 900).cols, 1)
  assert.equal(gridLayout(4, 800, 800).cols, 2)
  assert.equal(gridLayout(8, 976, 420).cols, 4)
  assert.equal(gridLayout(0, 100, 100).cols, 1)
  assert.equal(gridLayout(3, 0, 0).width, 0)
})
