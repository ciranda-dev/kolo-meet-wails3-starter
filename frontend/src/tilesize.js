/**
 * The size in device pixels of the largest picture on screen: the widest and the tallest of `sizes`
 * (CSS pixels) times the device pixel ratio. The Go side draws pictures for it (SetTileSize).
 */
export function largestTile(sizes, ratio) {
  let width = 0
  let height = 0
  for (const size of sizes) {
    width = Math.max(width, Number(size?.width) || 0)
    height = Math.max(height, Number(size?.height) || 0)
  }
  const r = Number.isFinite(ratio) && ratio > 0 ? ratio : 1
  return { width: Math.round(width * r), height: Math.round(height * r) }
}
