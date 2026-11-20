/**
 * The window's copy of the call, and the calls it has already held. Everything that brings a call
 * goes through `callReducer`: a `meet:call` event, the answer of `Call()`, and the answer of
 * `CreateCall` or `JoinCall`, which can arrive after later events. A copy is replaced only by
 * something newer, so the window never shows a call that no longer exists:
 *
 * - same call: only a higher `seq` wins, and nothing wins over `ended`;
 * - a call that ended or was put away never comes back;
 * - another call that the window held before is an old one, and is dropped;
 * - a call the window has never held is the new call.
 */
export const noCall = { call: null, seen: {}, over: {} }

function take(state, raw) {
  if (!raw || typeof raw.id !== 'string' || raw.id === '') {
    return state
  }
  // The window reads the people of a call as a list everywhere.
  const next = Array.isArray(raw.participants) ? raw : { ...raw, participants: [] }
  const { call } = state
  if (state.over[next.id] && call?.id !== next.id) {
    return state
  }
  if (call && call.id === next.id) {
    if (call.state === 'ended') {
      return state
    }
    if (Number.isFinite(call.seq) && Number.isFinite(next.seq) && next.seq <= call.seq) {
      return state
    }
  } else if (state.seen[next.id]) {
    return state
  }
  return {
    call: next,
    seen: { ...state.seen, [next.id]: true },
    over: next.state === 'ended' ? { ...state.over, [next.id]: true } : state.over,
  }
}

export function callReducer(state, action) {
  switch (action.type) {
    case 'call':
      return take(state, action.call)
    case 'dismiss':
      // Put the copy away (back to the lobby or the start). Its call is over for this window.
      return state.call ? { ...state, call: null, over: { ...state.over, [state.call.id]: true } } : state
    case 'invite':
      // A new invite, for the call that asked for it and only while that call is still running.
      return state.call && state.call.id === action.id && state.call.state === 'active' && action.invite
        ? { ...state, call: { ...state.call, invite: action.invite } }
        : state
    default:
      return state
  }
}
