/** The network as one quiet line under the sidebar: which network this is, then how it stands. No address is named. */
export function networkLine(t, mode, stand) {
  const which = ['public', 'own', 'local'].includes(mode) ? t(`settings.network.${mode}`) : ''
  return [which, stand].filter(Boolean).join(' · ')
}

/**
 * The tone of the dot beside a state. A network that is up and waiting for a call is fine, not a warning: only a call
 * that is connecting, a network that starts and an error stand out.
 */
export function stateTone(badge, network, fatal, pending) {
  return badge.tone === 'waiting' && network.state === 'ready' && !fatal && !pending ? 'live' : badge.tone
}

/** The title of the main bar for a view; the copy keys are bar.* and app.title. */
export function barTitleKey(view) {
  switch (view) {
    case 'home':
      return 'bar.home'
    case 'lobby':
      return 'bar.new'
    case 'joining':
    case 'call':
    case 'ended':
      return 'bar.call'
  }
  return 'app.title'
}

/** What the "+" can start now: nothing during a call or before the network is ready and the person has a name. */
export function canStartCall({ ready, named, inCall }) {
  return Boolean(ready && named && !inCall)
}
