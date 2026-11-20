const WIDEST = 16 / 9
const NARROWEST = 1
// A tile fills its cell, but never becomes a strip: a picture cut to less than this is not a face any more.
const WIDEST_TILE = 2.4
const NARROWEST_TILE = 0.7

/**
 * How `count` tiles share a box. The number of columns is the one that leaves the most room for a
 * picture (a shape between a square and 16:9) in each cell; each tile then takes its whole cell, so
 * the grid fills the box; a last row with fewer tiles shares the whole row among them (`lastWidth`).
 * Returns whole pixels, so rows never wrap by a rounding error.
 */
export function gridLayout(count, width, height, gap = 8) {
  const n = Math.max(1, Math.floor(count) || 1)
  let best = { cols: 1, rows: n, width: 0, height: 0 }
  let bestRoom = 0
  for (let cols = 1; cols <= n; cols++) {
    const rows = Math.ceil(n / cols)
    let w = (width - gap * (cols - 1)) / cols
    let h = (height - gap * (rows - 1)) / rows
    if (!(w > 0) || !(h > 0)) {
      continue
    }
    const room = w / h > WIDEST ? h * WIDEST * h : w / h < NARROWEST ? w * (w / NARROWEST) : w * h
    if (room > bestRoom) {
      bestRoom = room
      if (w / h > WIDEST_TILE) {
        w = h * WIDEST_TILE
      } else if (w / h < NARROWEST_TILE) {
        h = w / NARROWEST_TILE
      }
      best = { cols, rows, width: Math.floor(w), height: Math.floor(h) }
    }
  }
  const last = n - best.cols * (best.rows - 1)
  const wide = Math.floor(Math.min((width - gap * (last - 1)) / last, best.height * WIDEST_TILE))
  return { ...best, last, lastWidth: best.width > 0 ? Math.max(best.width, wide) : 0 }
}
