/** A comma list of the service (connection relays as host:port@key, or entry nodes) as a list. */
export function relayList(text) {
  return String(text ?? '').split(/[\s,]+/).map((s) => s.trim()).filter(Boolean)
}

/** One entry split at its last @: the address and the key, as typed. */
export function splitRelay(entry) {
  const at = entry.lastIndexOf('@')
  return at < 0 ? { addr: entry, key: '' } : { addr: entry.slice(0, at), key: entry.slice(at + 1) }
}

/**
 * One line about the listed relays: how many, and, during a call, how many hold a place for this
 * app. Empty when none is listed.
 */
export function relaySummary(t, statuses) {
  const list = statuses ?? []
  if (list.length === 0) {
    return ''
  }
  if (list.every((s) => s.state === 'idle')) {
    return t('relays.summary.idle', { n: list.length })
  }
  return t('relays.summary', { ready: list.filter((s) => s.state === 'reserved').length, n: list.length })
}

/** The relays that hold no place during a call, each with the reason the service gives. */
export function relayProblems(statuses) {
  return (statuses ?? []).filter((s) => s.state === 'not-reserved' && s.error)
}
