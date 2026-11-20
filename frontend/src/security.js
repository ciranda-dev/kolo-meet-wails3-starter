import { safeText } from './format.js'

/** The one mode this window knows the words of. Any other mode shows the service's own text. */
export const MODE = 'group-keys-host-relay'

/** Whether the window has its own words for this call: the known mode, and a name for who runs the relay. */
export function known(security) {
  return security?.mode === MODE && (security.operatorIsYou || Boolean(security.operator))
}

/** Whether every participant's keys have arrived. Only then may the window say "end-to-end encrypted". */
export function complete(security) {
  return security?.complete === true && !(security.waiting > 0)
}

/**
 * The line above the grid. For the known mode it is the contract's sentence in the language of the
 * window; for any other mode it is the service's line as it came. Empty before the call is active.
 * While someone waits for keys the line counts them and does not say "end-to-end".
 */
export function securityHeader(t, security) {
  if (!security || !security.header) {
    return ''
  }
  if (!known(security)) {
    return safeText(security.header)
  }
  const who = security.operatorIsYou ? 'host' : 'guest'
  const vars = { n: security.participants, k: security.waiting, name: safeText(security.operator) }
  return complete(security) ? t(`security.header.${who}`, vars) : t(`security.header.waiting.${who}`, vars)
}

// The service adds this sentence (security.go, detailConnectionRelays) when connection relays are listed.
const RELAYS = 'Each connection relay in your settings sees your key'

/**
 * The text behind the line, whole and in the order of the service: what the relay cannot see, who
 * could forge, what the relay sees, who can read the media, what a compromise means, who runs the
 * relay, while someone waits for keys that nothing of them is played, and, when the service says
 * so, what each connection relay sees.
 */
export function securityDetail(t, security) {
  if (!security || !security.detail) {
    return []
  }
  if (!known(security)) {
    return [safeText(security.detail)]
  }
  const list = [
    t('security.detail.cannot'),
    t('security.detail.forge'),
    t('security.detail.sees'),
    t('security.detail.members'),
    t('security.detail.compromise'),
    security.operatorIsYou ? t('security.detail.host') : t('security.detail.guest', { name: safeText(security.operator) }),
  ]
  if (!complete(security)) {
    list.push(t('security.detail.waiting'))
  }
  if (security.detail.includes(RELAYS)) {
    list.push(t('security.detail.relays'))
  }
  return list
}
