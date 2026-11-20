// WCAG 2.x contrast of every text and background pair of src/meet.css, in both themes, and of the ink of the avatars.
// Exits 1 when a pair is under its minimum, or when the two copies of the light theme differ.
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { AVATAR_COLORS } from '../src/avatars.js'

const css = readFileSync(join(fileURLToPath(import.meta.url), '..', '..', 'src', 'meet.css'), 'utf8')
const tokens = (block) => Object.fromEntries([...block.matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{6});/g)].map((m) => [m[1], m[2].toLowerCase()]))
const dark = tokens(css.match(/:root\s*\{([^}]*)\}/)[1])
const lightMedia = tokens(css.match(/:root:not\(\[data-theme="dark"\]\)\s*\{([^}]*)\}/)[1])
const light = tokens(css.match(/:root\[data-theme="light"\]\s*\{([^}]*)\}/)[1])

const lin = (c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4)
const lum = (hex) => {
  const [r, g, b] = [1, 3, 5].map((i) => lin(parseInt(hex.slice(i, i + 2), 16) / 255))
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
const ratio = (a, b) => {
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

// text, background, where, minimum
const pairs = [
  ['text', 'bg', 'text on the window', 4.5],
  ['text', 'panel', 'text on cards, header, tiles and bars', 4.5],
  ['text', 'soft', 'the initial on an avatar', 4.5],
  ['muted', 'bg', 'hints, a device button that is off', 4.5],
  ['muted', 'panel', 'labels, meta lines, tile states, history', 4.5],
  ['on-accent', 'accent', 'primary button, live badge, a round switch that is on', 4.5],
  ['on-accent', 'accent-2', 'primary button on hover', 4.5],
  ['danger', 'panel', 'error text on cards, Leave', 4.5],
  ['danger', 'bg', 'error text on the window', 4.5],
  ['plate-text', 'plate', 'the name plate on a picture', 4.5],
  ['accent', 'panel', 'ring arc, speaking ring, meter', 3],
  ['accent', 'bg', 'speaking ring on the window', 3],
  ['accent', 'plate', 'speaking ring next to a dark picture', 3],
  ['accent', 'ring-track', 'meter level on its track', 3],
  ['accent-2', 'panel', 'focus ring on cards', 3],
  ['accent-2', 'bg', 'focus ring on the window', 3],
  ['text', 'panel', 'people dots on the ring', 3],
  ['warn', 'panel', 'connecting dot and line of the call in the sidebar', 3],
  ['warn', 'bg', 'state dot on the card of the sidebar', 3],
  ['warn', 'active', 'Connecting, in the row of the call', 4.5],
  ['online', 'panel', 'state dot and text in the bar and the sidebar', 4.5],
  ['online', 'bg', 'network dot on the card of the sidebar', 3],
  ['online', 'active', 'In call, in the row of the call', 4.5],
  ['text', 'active', 'name of the call in the sidebar, Settings when chosen', 4.5],
  ['muted', 'active', 'quiet line in the row of the call', 4.5],
  ['muted', 'hover', 'History and Settings under the pointer', 4.5],
  ['text', 'hover', 'History and Settings under the pointer', 4.5],
  ['accent', 'active', 'icon of a row in the add menu', 3],
  ['on-accent', 'accent', 'the disc of the call in the sidebar', 4.5],
]

let failed = false
if (JSON.stringify(lightMedia) !== JSON.stringify(light)) {
  console.error('FAIL: the light tokens in the media query and in [data-theme="light"] differ')
  failed = true
}
for (const [name, theme] of [['dark', dark], ['light', { ...dark, ...light }], ['notch (dark on black)', { ...dark, bg: '#000000', panel: '#000000', soft: '#000000' }]]) {
  console.log(`\n${name}`)
  for (const [a, b, where, need] of pairs) {
    const r = ratio(theme[a], theme[b])
    const ok = r >= need
    failed ||= !ok
    console.log(`  ${a.padEnd(10)} ${theme[a]} on ${b.padEnd(10)} ${theme[b]}  ${r.toFixed(2).padStart(5)}:1  ${ok ? 'pass' : 'FAIL'} (${need})  ${where}`)
  }
}
// The ink of an avatar is fixed (the colors are light in both themes).
console.log('\navatars')
for (const color of AVATAR_COLORS) {
  const r = ratio('#06121f', color)
  const ok = r >= 4.5
  failed ||= !ok
  console.log(`  #06121f on ${color}  ${r.toFixed(2).padStart(5)}:1  ${ok ? 'pass' : 'FAIL'} (4.5)`)
}
process.exit(failed ? 1 : 0)
