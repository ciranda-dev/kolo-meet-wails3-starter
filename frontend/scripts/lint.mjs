// Checks the sources without any dependency: no em or en dash anywhere; every copy key that the code asks
// for exists and every key that exists is asked for; and the window keeps its privacy rules: no
// HTML from strings, no code from strings, no address outside the app, no camera or microphone of
// the browser, and no invite on the notch page.
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { fileURLToPath } from 'node:url'
import { messages } from '../src/i18n.js'

const root = join(fileURLToPath(import.meta.url), '..', '..')
const files = []
const walk = (dir) => {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) {
      walk(path)
    } else if (/\.(jsx?|mjs|ts|css|html|json)$/.test(name)) {
      files.push(path)
    }
  }
}
for (const dir of ['src', 'dev', 'notch', 'test', 'scripts']) {
  walk(join(root, dir))
}
files.push(join(root, 'index.html'))

// Words that must not be in the window or its mock, built from pieces so this file passes itself.
const banned = [
  ['HTML from a string', new RegExp(['inner' + 'HTML', 'outer' + 'HTML', 'dangerously' + 'Set', 'insertAdjacent' + 'HTML', 'document\\.' + 'write'].join('|'))],
  ['code from a string', new RegExp(['\\bev' + 'al\\(', 'new ' + 'Function\\('].join('|'))],
  ['the camera or microphone of the browser', new RegExp(['getUser' + 'Media', 'media' + 'Devices', 'getDisplay' + 'Media'].join('|'))],
  ['an address outside the app', /https?:\/\/(?!127\.0\.0\.1[:/])/],
]

const problems = []
const used = new Set()
for (const file of files) {
  const text = readFileSync(file, 'utf8')
  const name = relative(root, file)
  const shipped = name.startsWith('src/') || name.startsWith('dev/mock') || name.startsWith('notch/') || name === 'index.html'
  text.split('\n').forEach((line, i) => {
    if (line.includes(String.fromCharCode(0x2014)) || line.includes(String.fromCharCode(0x2013))) {
      problems.push(`${name}:${i + 1}: em or en dash`)
    }
    if (shipped) {
      for (const [what, pattern] of banned) {
        if (pattern.test(line)) {
          problems.push(`${name}:${i + 1}: ${what}`)
        }
      }
    }
  })
  if (name === 'src/Notch.jsx') {
    // The notch page may know that an invite exists and ask the service to copy it, nothing more.
    const rest = text.replace(/hasInvite|NotchCopyInvite|notch\.copyInvite|never holds the invite/g, '')
    if (/invite/i.test(rest)) {
      problems.push(`${name}: the notch page must not hold or read an invite`)
    }
  }
  if (name.endsWith('.html') && !/<meta http-equiv="Content-Security-Policy" content="default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self';/.test(text)) {
    problems.push(`${name}: no content security policy`)
  }
  if (name.endsWith('.html') && /<script(?![^>]*\bsrc=)|<style|\sstyle=|\son\w+=/.test(text)) {
    problems.push(`${name}: inline script, style or handler (a content security policy would block it)`)
  }
  if (!name.startsWith('src/') || name.startsWith('src/messages/')) {
    continue
  }
  // Any quoted dotted word counts as a use: keys are also picked with a condition inside t(...).
  for (const m of text.matchAll(/'([a-z][\w]*(?:\.[\w-]+)+)'/g)) {
    used.add(m[1])
  }
  // Keys built from a state, a device or a reason: `phase.${x}` uses every key under that prefix.
  for (const m of text.matchAll(/`([a-z][\w]*\.[\w.-]*)\$\{/g)) {
    used.add(`${m[1]}*`)
  }
}

const keys = Object.keys(messages.en)
const has = (key) => keys.includes(key) || keys.includes(`${key}.one`)
for (const key of used) {
  if (!key.endsWith('*') && !has(key) && !/\.(js|jsx|css|ts)$/.test(key)) {
    problems.push(`copy key '${key}' is used but not in src/messages/en.js`)
  }
}
const prefixes = [...used].filter((k) => k.endsWith('*')).map((k) => k.slice(0, -1))
for (const key of keys) {
  const base = key.replace(/\.(one|other)$/, '')
  if (!used.has(key) && !used.has(base) && !prefixes.some((p) => key.startsWith(p))) {
    problems.push(`copy key '${key}' is never used`)
  }
}

if (problems.length > 0) {
  console.error(problems.join('\n'))
  process.exit(1)
}
console.log(`lint: ${files.length} files, ${keys.length} copy keys, no problems`)
