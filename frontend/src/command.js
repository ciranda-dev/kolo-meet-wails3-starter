import { relayList, splitRelay } from './relays.js'

// The app as the README starts it.
const APP = './bin/kolo-meet'

/** One argument of a shell command: as it is when it is a plain word, else in single quotes. */
export function shellArg(value) {
  const text = String(value ?? '')
  return /^[\w./:@%+=,-]+$/.test(text) ? text : `'${text.replaceAll("'", "'\\''")}'`
}

const command = (settings, args) => [APP, ...(settings?.dataDir ? ['-data', shellArg(settings.dataDir)] : []), ...args].join(' ')

/** The command that starts this window right after an error, or '' when the error has none. */
export function fixCommand(settings, err) {
  if (err?.reason !== 'first-node-in-use' || !err.detail) {
    return ''
  }
  return command(settings, ['-bootstrap', shellArg(err.detail)])
}

/** The network in use: "public" (nothing to start it with), "own" (-bootstrap, -first) or "local" (-local). */
export const networkMode = (settings) => settings?.networkMode || 'public'

// The entry nodes of an own network besides the one this app runs with -first.
const otherNodes = (settings) => (networkMode(settings) === 'own' ? relayList(settings?.entryNodes).filter((n) => n !== settings?.firstNode) : [])

/**
 * The command that starts this window with the network it uses now, as its parts: the app, then one flag with its
 * value each. The public network, the default, has no flag.
 */
export function networkCommandParts(settings) {
  if (!settings) {
    return []
  }
  const parts = [APP]
  if (settings.dataDir) {
    parts.push(`-data ${shellArg(settings.dataDir)}`)
  }
  if (networkMode(settings) === 'local') {
    parts.push('-local')
  } else {
    if (settings.firstNode) {
      parts.push(`-first ${shellArg(settings.firstNode)}`)
    }
    const nodes = otherNodes(settings)
    if (nodes.length > 0) {
      parts.push(`-bootstrap ${shellArg(nodes.join(','))}`)
    }
  }
  for (const relay of relayList(settings.connectionRelays)) {
    parts.push(`-relay ${shellArg(relay)}`)
  }
  return parts
}

/** The same command on one line. */
export const networkCommand = (settings) => networkCommandParts(settings).join(' ')

const NODES_FROM = { flag: '-bootstrap', env: 'KOLO_BOOTSTRAP', local: '-local' }
const RELAYS_FROM = { flag: '-relay', file: '-relay-file', 'flag-and-file': '-relay, -relay-file', env: 'KOLO_MEET_RELAYS' }

/** The network in use, its entry nodes and the connection relays (addresses only), each with where it came from. */
export function networkSources(settings) {
  const mode = networkMode(settings)
  const nodes = []
  if (mode === 'local') {
    nodes.push({ list: relayList(settings.entryNodes), from: '-local' })
  }
  if (mode === 'own' && settings?.firstNode) {
    nodes.push({ list: [settings.firstNode], from: '-first' })
  }
  const others = otherNodes(settings)
  if (others.length > 0) {
    nodes.push({ list: others, from: NODES_FROM[settings.entryNodesFrom] ?? '' })
  }
  const relays = relayList(settings?.connectionRelays).map((r) => splitRelay(r).addr)
  return { mode, nodes, relays: relays.length > 0 ? { list: relays, from: RELAYS_FROM[settings.connectionRelaysFrom] ?? '' } : null }
}
