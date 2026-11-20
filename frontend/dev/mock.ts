// A stand-in for the Go service, for design work in a browser: `npm run dev`, then open
// http://127.0.0.1:9245/?mock (a live simulation) or ?mock=<scene> (one frozen state).
// It has the shape of the generated bindings plus the two runtime objects the window uses.
// src/service.js only loads it in a dev server, so a production build never includes it.
// It never touches a camera or a microphone: pictures are drawn on a canvas and levels are made up.

interface MeetError {
  kind: string
  reason: string
  detail: string
  cause: string
  message: string
}

interface MediaState {
  state: 'off' | 'on' | 'error'
  deviceId: string
  error: MeetError | null
  hint?: MeetError | null
}

interface Participant {
  id: string
  name: string
  self: boolean
  host: boolean
  state: 'waiting-keys' | 'live' | 'media-stopped'
  audioOn: boolean
  videoOn: boolean
  speaking: boolean
  joined: number
  link: 'direct' | 'relayed' | ''
  connectionRelay: string
}

interface Call {
  id: string
  role: 'host' | 'guest'
  state: 'creating' | 'joining' | 'connecting' | 'active' | 'ended'
  seq: number
  phase: string
  invite: string
  self: string
  host: string
  participants: Participant[]
  max: number
  microphone: MediaState
  camera: MediaState
  speaker: MediaState
  security: { mode: string; header: string; detail: string; operator: string; operatorIsYou: boolean; participants: number; waiting: number; complete: boolean }
  started: number
  finished: number
  error: MeetError | null
  warning: MeetError | null
  relayLink: 'direct' | 'relayed' | ''
  relayVia: string
}

interface RelayStatus {
  addr: string
  key: string
  state: 'idle' | 'reserved' | 'not-reserved'
  error: MeetError | null
}

interface HistoryItem {
  id: string
  role: 'host' | 'guest'
  result: 'done' | 'failed'
  message: string
  participants: number
  seconds: number
  at: number
  error: MeetError | null
}

const query = new URLSearchParams(window.location.search)
const scene = query.get('mock') || 'live'
const isNotch = window.location.pathname.startsWith('/notch')
const now = Date.now()
const frozen = scene !== 'live'

const INVITE = 'koloinv0qx7Zt2mVb9LkR4sWd8YcA1fGh6JnP3uTe5XoBi0MyKvNrCa7DwEzQ2lHs4FgUj9pSx6OtVb1ImYk8ZcRd3WnAe5LqTf0GhJu2MoPv7XyBi4KsNz6CaDwEr9lHt1FgUj3pSx8Ot'
// Names a hostile participant could use: a right-to-left override that turns one name into
// another, zero-width characters, text that looks like markup, and a name as wide as it may be.
const SPOOF_NAME = 'Marta \u202eariervilO'
const HOSTILE_NAME = '<img src=x onerror=alert(1)>\u200b\u2066x\u2069\ufeff'
const WIDE_NAME = 'WWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWWW'
const NAMES = ['Ana Ribeiro', 'Marta Oliveira', 'Tomás Herrera', 'Yuki Tanaka', 'Chidi Okafor', 'Léa Fontaine', 'Rahul Mehta', 'Sofia Lindqvist']
const HUES = [212, 28, 345, 46, 196, 268, 12, 232]

// Every sentence of the service, by reason, so any error can be put on screen: ?mock=ended-<reason>.
const REASONS: Record<string, [string, string]> = {
  cancelled: ['cancelled', 'Cancelled.'],
  'invalid-invite': ['invalid-invite', 'This is not a call invite. Copy the whole invite, it starts with koloinv0.'],
  'host-not-found': ['not-found', 'Could not find the person who invited you. They must be online with Kolo Meet open and on the same network (public, your own, or this computer).'],
  'host-lost': ['not-found', 'The connection to the host ended before it answered. Try again: asking twice is safe.'],
  'join-denied': ['refused', 'The call was full when you asked, or the host did not let you in. Ask the host for a new invite.'],
  'invite-expired': ['expired', 'This invite has expired. Ask for a new one.'],
  'invite-used-up': ['expired', 'This invite has no uses left. Ask for a new one.'],
  'invite-revoked': ['expired', 'The host ended this call or revoked the invite. Ask for a new one.'],
  'invite-unknown': ['invalid-invite', 'The host does not recognize this invite. Check that you pasted the whole invite.'],
  'join-rate-limited': ['busy', 'Too many attempts. Wait a minute and try again.'],
  'host-busy': ['busy', 'The host is busy. Try again in a moment.'],
  'bad-grant': ['refused', 'The host sent an answer this version of Kolo Meet cannot use. Ask for a new invite.'],
  'relay-unreachable': ['network', "Could not connect to the call's relay: {cause}."],
  'call-unavailable': ['network', 'Could not start the call: {cause}.'],
  'too-many-invites': ['busy', 'This call has {detail} invites already. Wait for one to expire or end the call.'],
  'busy-call': ['busy', 'You are in a call already. Leave it first.'],
  'busy-reconnect': ['busy', 'Leave the call first.'],
  'no-call': ['busy', 'You are not in a call.'],
  'call-still-ending': ['busy', 'The call is still ending. Try again in a moment.'],
  'call-full': ['refused', "The call is full, or the host's app closed your connection while you were joining. Ask the host."],
  'microphone-silent': ['device', "The microphone sends only silence. Check that Kolo Meet may use it in your system's privacy settings and that it is not muted."],
  'call-ended-host-left': ['ended', 'The call ended: the host left or lost the connection.'],
  'call-ended-removed': ['ended', 'The host removed you from this call.'],
  'rejoin-refused': ['refused', 'You left this call or were removed from it, so this invite no longer lets you in. Ask the host for a new invite.'],
  'not-host': ['refused', 'Only the host can remove someone from the call.'],
  'remove-self': ['refused', 'You cannot remove yourself. End the call to leave it.'],
  'participant-unknown': ['not-found', 'That person is not in the call.'],
  'call-ended-lost': ['ended', 'The call ended: the connection was lost.'],
  'call-ended-closed': ['ended', 'The call ended because Kolo Meet closed.'],
  'network-not-ready': ['network', 'The network is not ready yet. Wait a moment, or press Try again if Kolo Meet shows an error.'],
  'public-unreachable': ['network', 'Could not reach the public Kolo network: {cause}. Check your internet connection and press Try again, or choose another network: start Kolo Meet with -bootstrap and the entry nodes of your own network, or with -local to use this computer only.'],
  'local-port-taken': ['network', 'Another program uses {detail} on this computer, so the local network cannot start. Close that program and press Try again, or start Kolo Meet without -local to use the public network, or with -bootstrap and the entry nodes of your own network.'],
  'bootstrap-failed': ['network', 'Could not reach the entry nodes ({detail}): {cause}. Check -bootstrap or KOLO_BOOTSTRAP, or start Kolo Meet without it to use the public network.'],
  'first-node-in-use': ['network', 'Another window, probably Kolo Meet, already runs the entry node on {detail}. Start this window with -bootstrap {detail} instead of -first.'],
  'lookup-failed': ['network', 'Could not look for the host: {cause}.'],
  'camera-denied': ['permission', "Kolo Meet may not use the camera. Allow it in your system's privacy settings, then try again."],
  'microphone-denied': ['permission', "Kolo Meet may not use the microphone. Allow it in your system's privacy settings, then try again."],
  'camera-none': ['device', 'No camera was found.'],
  'microphone-restricted': ['permission', 'A policy of this computer, such as parental controls or device management, does not let Kolo Meet use the microphone.'],
  'microphone-none': ['device', 'No microphone was found.'],
  'speaker-none': ['device', 'No speaker was found.'],
  'camera-lost': ['device', 'The camera stopped working or was unplugged.'],
  'microphone-lost': ['device', 'The microphone stopped working or was unplugged.'],
  'camera-failed': ['device', 'Could not use the camera: {cause}.'],
  'device-unknown': ['device', 'That device is not in the list. Refresh the devices and choose again.'],
  'codec-unavailable': ['media', 'Video or audio coding is not available on this computer: {cause}.'],
  'name-empty': ['name', 'Write a name other people will see in the call.'],
  'name-too-long': ['name', 'The name is too long. Use at most {detail} characters.'],
  'data-dir-open': ['config', 'The data directory {detail} is open to other users. Make it private (chmod 700) or choose another one.'],
  'connection-relay-not-allowed': ['network', 'The connection relay {detail} does not let Kolo Meet hold a place there. Ask its operator to allow your key (the kolo-id line in the terminal), or start Kolo Meet without it.'],
  'connection-relay-full': ['network', 'The connection relay {detail} is full. Kolo Meet tries again; your other connection relays still work.'],
  'connection-relay-unreachable': ['network', 'Could not reach the connection relay {detail}. Kolo Meet tries again.'],
  'connection-relay-none-shared': ['network', 'You and the host have no connection relay in common. Two people who cannot connect directly need a connection relay that both of them start Kolo Meet with (-relay), or they stay "waiting for keys" for each other. Ask the host for one of theirs.'],
  'host-unreachable': ['not-found', "Could not connect to the host's app, directly or through a connection relay. The host may have closed it or ended the call; if not, and you are on different networks, you and the host need the same connection relay (-relay)."],
  'relay-no-common': ['network', "Could not connect to the host's app directly, and you and the host have no connection relay in common. Start Kolo Meet again with one of the host's connection relays (-relay), then join again."],
  internal: ['internal', 'Something went wrong: {cause}.'],
}

function meetErr(reason: string, detail = '', cause = ''): MeetError {
  const [kind, text] = REASONS[reason] ?? ['internal', `Unknown reason ${reason}.`]
  return { kind, reason, detail, cause, message: text.replaceAll('{detail}', detail).replaceAll('{cause}', cause) }
}

/** A rejected call, shaped like the one the Wails runtime throws: the Go error is its cause. */
function reject(error: MeetError): never {
  throw Object.assign(new Error(error.message), { cause: error })
}

type Handler = (event: { name: string; data: unknown }) => void
const handlers = new Map<string, Set<Handler>>()
const emit = (name: string, data: unknown) => handlers.get(name)?.forEach((cb) => cb({ name, data }))

export const Events = {
  On(name: string, cb: Handler) {
    if (!handlers.has(name)) {
      handlers.set(name, new Set())
    }
    handlers.get(name)!.add(cb)
    return () => handlers.get(name)?.delete(cb)
  },
}

let clipboard = INVITE
export const Clipboard = {
  async SetText(text: string) {
    clipboard = text
  },
  async Text() {
    return clipboard
  },
}

const OFF: MediaState = { state: 'off', deviceId: '', error: null }
const ON: MediaState = { state: 'on', deviceId: '', error: null }
const broken = (reason: string, cause = ''): MediaState => ({ state: 'error', deviceId: '', error: meetErr(reason, '', cause) })

let settings = {
  networkMode: 'public',
  entryNodes: '',
  entryNodesFrom: 'public',
  connectionRelays: '',
  connectionRelaysFrom: 'none',
  firstNode: '',
  dataDir: '/Users/ana/Library/Application Support/kolo-meet',
  listen: '0.0.0.0:0',
  maxParticipants: 8,
  showInNotch: true,
  displayName: 'Ana Ribeiro',
  cameraId: '',
  microphoneId: '',
  speakerId: '',
}
let network: { state: string; message: string; error: MeetError | null; mode: string; entryNodes: number; firstNode: string; relays: RelayStatus[] } = { state: 'ready', message: '', error: null, mode: 'public', entryNodes: 2, firstNode: '', relays: [] }
const RELAY_A = '203.0.113.20:7000@3b6a27bcceb6a42d62a3a8d02a6f0d73653215771de243a63ac048a18b59da29'
const RELAY_B = 'relay.example.org:7000@e3c1f0a9b8d7c6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c3d2e1'
function relayStatuses(state: RelayStatus['state'] = 'idle'): RelayStatus[] {
  return settings.connectionRelays.split(',').filter(Boolean).map((entry) => {
    const at = entry.lastIndexOf('@')
    return { addr: entry.slice(0, at), key: entry.slice(at + 1), state, error: null }
  })
}
let devices = {
  cameras: [
    { id: 'cam-1', name: 'Built-in camera', default: true },
    { id: 'cam-2', name: 'Desk camera 1080p', default: false },
  ],
  microphones: [
    { id: 'mic-1', name: 'Built-in microphone', default: true },
    { id: 'mic-2', name: 'USB headset', default: false },
  ],
  speakers: [
    { id: 'spk-1', name: 'Built-in speakers', default: true },
    { id: 'spk-2', name: 'USB headset', default: false },
  ],
}
let lobby: { microphone: MediaState; camera: MediaState; speaker: MediaState } = { microphone: OFF, camera: OFF, speaker: OFF }
let deny: Record<string, string> = {}
let call: Call | null = null
let startFails: MeetError | null = null

const sampleHistory: HistoryItem[] = [
  { id: 'h1', role: 'host', result: 'done', message: '', participants: 4, seconds: 2764, at: now - 40 * 60 * 1000, error: null },
  { id: 'h2', role: 'guest', result: 'done', message: '', participants: 2, seconds: 612, at: now - 5 * 3600 * 1000, error: null },
  { id: 'h3', role: 'guest', result: 'failed', message: meetErr('host-not-found').message, participants: 1, seconds: 0, at: now - 27 * 3600 * 1000, error: meetErr('host-not-found') },
  { id: 'h4', role: 'host', result: 'done', message: '', participants: 8, seconds: 5403, at: now - 3 * 86400 * 1000, error: null },
  { id: 'h5', role: 'guest', result: 'failed', message: meetErr('call-ended-host-left').message, participants: 3, seconds: 95, at: now - 6 * 86400 * 1000, error: meetErr('call-ended-host-left') },
  { id: 'h6', role: 'host', result: 'done', message: '', participants: 1, seconds: 48, at: now - 9 * 86400 * 1000, error: null },
]
let history: HistoryItem[] = sampleHistory

function person(index: number, patch: Partial<Participant> = {}): Participant {
  return { id: `p${index}`.padEnd(8, '0'), name: NAMES[index % NAMES.length], self: index === 0, host: false, state: 'live', audioOn: true, videoOn: true, speaking: false, joined: now - 60000, link: index === 0 ? '' : 'direct', connectionRelay: '', ...patch }
}

const DETAIL = "The relay cannot see audio, video or names, not even how long a name is. Anyone in the call could, with the relay's help, make audio, video or names that seem to come from someone else. The relay sees who is in the call, who watches whom, when each person sends and how much, when microphones and cameras are off, and the room ID; it can drop or delay media. Anyone in the call can read everyone's media. There is no protection for the current key period if a device is compromised, and no recovery after a compromise."

const RELAYS_DETAIL = ' Each connection relay in your settings sees your key, your address and when you are in a call, and, for the connections it carries, who connects to whom, when and how much. It cannot read them: they are encrypted end to end between the two apps.'

const NO_SECURITY = { mode: '', header: '', detail: '', operator: '', operatorIsYou: false, participants: 0, waiting: 0, complete: false }

function security(c: Call): Call['security'] {
  if (c.state !== 'active') {
    return NO_SECURITY
  }
  const n = c.participants.length
  const waiting = c.participants.filter((p) => p.state === 'waiting-keys').length
  const operator = c.participants.find((p) => p.host)?.name ?? 'Marta Oliveira'
  const you = c.role === 'host'
  const by = you ? 'you' : operator
  const run = you ? ' You run the relay, so you could make audio, video or names that seem to come from someone else.' : ` ${operator} runs the relay, so ${operator} could make audio, video or names that seem to come from someone else.`
  const header = waiting === 0 ? `${n} in call, end-to-end encrypted, through relay run by ${by}` : `${n} in call, ${waiting} waiting for keys, through relay run by ${by}`
  const relays = settings.connectionRelays ? RELAYS_DETAIL : ''
  return { mode: 'group-keys-host-relay', header, detail: DETAIL + run + (waiting === 0 ? '' : ' Nothing from a person who is waiting for keys is played.') + relays, operator, operatorIsYou: you, participants: n, waiting, complete: waiting === 0 }
}

function makeCall(patch: Partial<Call>): Call {
  const role = patch.role ?? 'host'
  const people = patch.participants ?? [person(0)]
  const hostIndex = role === 'host' ? 0 : 1
  const participants = people.map((p, i) => ({ ...p, host: i === Math.min(hostIndex, people.length - 1) && (role === 'host' || people.length > 1), audioOn: p.self ? lobby.microphone.state === 'on' : p.audioOn, videoOn: p.self ? lobby.camera.state === 'on' : p.videoOn }))
  const base: Call = {
    id: `c${Math.random().toString(36).slice(2, 8)}`,
    role,
    state: 'active',
    seq: 1,
    phase: '',
    invite: '',
    self: participants[0]?.id ?? '',
    host: participants.find((p) => p.host)?.id ?? '',
    max: settings.maxParticipants,
    microphone: lobby.microphone,
    camera: lobby.camera,
    speaker: ON,
    started: now - 754000,
    finished: 0,
    error: null,
    warning: null,
    relayLink: role === 'guest' && (patch.state ?? 'active') === 'active' ? 'direct' : '',
    relayVia: '',
    ...patch,
    participants,
    security: NO_SECURITY,
  }
  if (base.state === 'active' && role === 'host' && !base.invite) {
    base.invite = INVITE
  }
  if (base.state !== 'active' && base.state !== 'ended') {
    base.started = 0
    base.speaker = OFF
  }
  base.security = security(base)
  return base
}

const group = (n: number, role: 'host' | 'guest' = 'guest') => makeCall({ role, participants: Array.from({ length: n }, (_, i) => person(i)) })

function endedCall(reason: string, role: 'host' | 'guest', wasActive: boolean): Call {
  const detail = reason === 'too-many-invites' ? '8' : reason === 'name-too-long' ? '48' : reason === 'bootstrap-failed' ? '203.0.113.10:4000' : reason.startsWith('connection-relay-') && reason !== 'connection-relay-none-shared' ? '203.0.113.20:7000' : ''
  const cause = REASONS[reason]?.[1].includes('{cause}') ? 'context deadline exceeded' : ''
  return makeCall({ id: 'ended1', role, state: 'ended', participants: wasActive ? [person(0)] : [], started: wasActive ? now - 754000 : 0, finished: now, error: reason === 'left' ? null : meetErr(reason, detail, cause) })
}

// One frozen state per scene, so every screen can be looked at without the Go side.
const scenes: Record<string, () => void> = {
  live() {},
  home() {},
  'home-empty'() {
    history = []
  },
  name() {
    settings = { ...settings, displayName: '' }
    history = []
  },
  starting() {
    network = { state: 'starting', message: '', error: null, mode: 'public', entryNodes: 2, firstNode: '' }
  },
  'public-unreachable'() {
    const error = meetErr('public-unreachable', '', 'no entry node answered')
    network = { state: 'failed', message: error.message, error, mode: 'public', entryNodes: 2, firstNode: '' }
  },
  own() {
    settings = { ...settings, networkMode: 'own', entryNodes: '203.0.113.10:4000,entry.example.org:4000', entryNodesFrom: 'flag' }
    network = { ...network, mode: 'own' }
  },
  local() {
    settings = { ...settings, networkMode: 'local', entryNodes: '127.0.0.1:7451', entryNodesFrom: 'local' }
    network = { ...network, mode: 'local', entryNodes: 1 }
  },
  'first-in-use'() {
    settings = { ...settings, networkMode: 'own', entryNodes: '127.0.0.1:4000', entryNodesFrom: 'first', firstNode: '127.0.0.1:4000', dataDir: '/tmp/kolo-meet-demo/guest' }
    const error = meetErr('first-node-in-use', '127.0.0.1:4000', 'kolotransport: listen: listen udp4 127.0.0.1:4000: bind: address already in use')
    network = { state: 'failed', message: error.message, error, mode: 'own', entryNodes: 1, firstNode: '127.0.0.1:4000' }
  },
  'net-failed'() {
    const error = meetErr('bootstrap-failed', '203.0.113.10:4000, entry.example.org:4000', 'timeout')
    settings = { ...settings, networkMode: 'own', entryNodes: '203.0.113.10:4000,entry.example.org:4000', entryNodesFrom: 'flag' }
    network = { state: 'failed', message: error.message, error, mode: 'own', entryNodes: 2, firstNode: '' }
  },
  fatal() {
    startFails = meetErr('data-dir-open', '/Users/ana/Library/Application Support/kolo-meet')
  },
  relays() {
    settings = { ...settings, connectionRelays: `${RELAY_A},${RELAY_B}`, connectionRelaysFrom: 'flag' }
    network = { ...network, relays: relayStatuses() }
  },
  'relays-flag'() {
    settings = { ...settings, connectionRelays: RELAY_A, connectionRelaysFrom: 'flag' }
    network = { ...network, relays: relayStatuses() }
  },
  'call-relays'() {
    settings = { ...settings, connectionRelays: `${RELAY_A},${RELAY_B}`, connectionRelaysFrom: 'flag' }
    const [first, second] = relayStatuses('reserved')
    network = { ...network, relays: [first, { ...second, state: 'not-reserved', error: meetErr('connection-relay-unreachable', second.addr, 'timeout') }] }
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = makeCall({
      role: 'guest',
      relayLink: 'relayed',
      relayVia: first.addr,
      participants: [person(0), person(1), person(2, { link: 'relayed', connectionRelay: first.addr }), person(3, { link: 'relayed', connectionRelay: first.addr, videoOn: false })],
    })
  },
  'call-own'() {
    settings = { ...settings, networkMode: 'own', entryNodes: '203.0.113.10:4000,entry.example.org:4000', entryNodesFrom: 'flag' }
    network = { ...network, mode: 'own' }
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(4)
  },
  'call-local'() {
    settings = { ...settings, networkMode: 'local', entryNodes: '127.0.0.1:7451', entryNodesFrom: 'local' }
    network = { ...network, mode: 'local', entryNodes: 1 }
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(3, 'host')
  },
  'call-relay-warning'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = makeCall({
      role: 'guest',
      warning: meetErr('connection-relay-none-shared'),
      participants: [person(0), person(1), person(2, { state: 'waiting-keys', audioOn: false, videoOn: false, joined: now - 45000, link: '' })],
    })
  },
  'joining-relays'() {
    settings = { ...settings, connectionRelays: RELAY_A, connectionRelaysFrom: 'flag' }
    network = { ...network, relays: relayStatuses() }
    call = makeCall({ role: 'guest', state: 'joining', phase: 'finding-host' })
  },
  flag() {
    settings = { ...settings, networkMode: 'own', entryNodes: '127.0.0.1:4000,127.0.0.1:5000', entryNodesFrom: 'flag', firstNode: '127.0.0.1:4000' }
    network = { ...network, mode: 'own' }
  },
  'lobby-on'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
  },
  'lobby-denied'() {
    lobby = { microphone: broken('microphone-denied'), camera: broken('camera-denied'), speaker: OFF }
    deny = { camera: 'camera-denied', microphone: 'microphone-denied' }
  },
  'lobby-none'() {
    devices = { cameras: [], microphones: [], speakers: [] }
    lobby = { microphone: broken('microphone-none'), camera: broken('camera-none'), speaker: OFF }
    deny = { camera: 'camera-none', microphone: 'microphone-none' }
  },
  'lobby-ask'() {},
  'lobby-restricted'() {},
  'lobby-lost'() {
    lobby = { microphone: ON, camera: broken('camera-lost'), speaker: OFF }
  },
  creating() {
    call = makeCall({ role: 'host', state: 'creating' })
  },
  joining() {
    call = makeCall({ role: 'guest', state: 'joining', phase: 'finding-host' })
  },
  'waiting-host'() {
    call = makeCall({ role: 'guest', state: 'joining', phase: 'waiting-for-host' })
  },
  connecting() {
    call = makeCall({ role: 'guest', state: 'connecting', phase: 'relay' })
  },
  'call-1'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(1, 'host')
  },
  'call-1-guest'() {
    lobby = { microphone: ON, camera: OFF, speaker: OFF }
    call = makeCall({ role: 'guest', participants: [person(0)] })
  },
  'call-states'() {
    lobby = { microphone: OFF, camera: ON, speaker: OFF }
    call = makeCall({
      role: 'guest',
      participants: [
        person(0),
        person(1),
        person(2, { videoOn: false }),
        person(3, { state: 'waiting-keys', audioOn: false, videoOn: false, joined: now - 600 }),
        person(4, { state: 'waiting-keys', audioOn: false, videoOn: false, joined: now - 45000 }),
        person(5, { state: 'media-stopped', audioOn: false, videoOn: false }),
        person(6, { audioOn: false }),
      ],
    })
  },
  'call-hostile'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = makeCall({
      role: 'guest',
      participants: [person(0), person(1, { name: SPOOF_NAME }), person(2, { name: HOSTILE_NAME, videoOn: false }), person(3, { name: WIDE_NAME }), person(4, { name: '', videoOn: false }), person(5, { name: 'عبد الرحمن الكاتب' })],
    })
  },
  'call-denied'() {
    lobby = { microphone: broken('microphone-denied'), camera: broken('camera-lost'), speaker: OFF }
    deny = { microphone: 'microphone-denied' }
    call = group(3)
  },
  'call-silent'() {
    lobby = { microphone: { ...ON, hint: meetErr('microphone-silent') }, camera: ON, speaker: OFF }
    call = group(3)
  },
  'lobby-silent'() {
    lobby = { microphone: { ...ON, hint: meetErr('microphone-silent') }, camera: ON, speaker: OFF }
  },
  'call-stuck'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(3)
  },
  'call-toast'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(3, 'host')
    window.setTimeout(() => emit('meet:participant', { kind: 'left', id: 'p9', name: 'Yuki Tanaka', message: 'Yuki Tanaka no longer receives your media.' }), 600)
  },
  'call-unknown-mode'() {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(2)
    call.security = { ...call.security, mode: 'some-later-mode', header: '2 in call, a line this window does not know', detail: 'The words of the service, shown as they came.' }
  },
}
for (let n = 2; n <= 8; n++) {
  scenes[`call-${n}`] = () => {
    lobby = { microphone: ON, camera: ON, speaker: OFF }
    call = group(n, n % 2 === 0 ? 'guest' : 'host')
  }
}
if (scene.startsWith('ended-')) {
  const rest = scene.slice(6)
  const active = rest === 'left' || rest.startsWith('call-ended')
  const hostOnly = rest === 'call-unavailable' || rest === 'codec-unavailable'
  call = endedCall(rest, hostOnly || rest === 'left' ? 'host' : 'guest', active)
  if (active) {
    history = [{ id: 'ended1', role: call.role, result: call.error ? 'failed' : 'done', message: call.error?.message ?? '', participants: 4, seconds: 754, at: now, error: call.error }, ...history]
  }
} else {
  ;(scenes[scene] ?? scenes.live)()
}
if (call && scene !== 'call-unknown-mode') {
  // The scene set the devices after the call was made in some cases: rebuild from the lobby.
  call = makeCall({ ...call, microphone: lobby.microphone, camera: lobby.camera })
}

const pause = (ms: number) => new Promise((resolve) => window.setTimeout(resolve, ms))

// Test pictures: a head and shoulders that sway on a tinted ground, with a moving band and a
// frame counter, so a stuck or mirrored picture is easy to see. Drawn here; no camera is read.
const canvas = document.createElement('canvas')
canvas.width = 320
canvas.height = 240
const pen = canvas.getContext('2d')!
let tick = 0

function picture(index: number, label: string): string {
  const hue = HUES[index % HUES.length]
  const sway = Math.sin((tick + index * 9) / 11) * 14
  const ground = pen.createLinearGradient(0, 0, 320, 240)
  ground.addColorStop(0, `hsl(${hue} 34% 24%)`)
  ground.addColorStop(1, `hsl(${hue + 18} 30% 12%)`)
  pen.fillStyle = ground
  pen.fillRect(0, 0, 320, 240)
  pen.fillStyle = `hsl(${hue} 40% 34% / 0.5)`
  const band = ((tick * 5 + index * 60) % 480) - 120
  pen.beginPath()
  pen.moveTo(band, 0)
  pen.lineTo(band + 70, 0)
  pen.lineTo(band - 10, 240)
  pen.lineTo(band - 80, 240)
  pen.fill()
  pen.fillStyle = `hsl(${hue} 30% 70%)`
  pen.beginPath()
  pen.arc(160 + sway, 104, 40, 0, Math.PI * 2)
  pen.fill()
  pen.beginPath()
  pen.ellipse(160 + sway * 0.6, 250, 92, 84, 0, 0, Math.PI * 2)
  pen.fill()
  pen.fillStyle = 'rgba(255, 255, 255, 0.72)'
  pen.font = '11px ui-monospace, Menlo, monospace'
  pen.fillText(`${label} · test picture ${String(tick % 1000).padStart(3, '0')}`, 10, 18)
  return canvas.toDataURL('image/jpeg', 0.7).split(',')[1]
}

function pump() {
  tick++
  if (isNotch) {
    return
  }
  if ((call?.state === 'active' ? call.camera : lobby.camera).state === 'on') {
    emit('meet:frame', { id: 'self', seq: tick, width: 320, height: 240, jpeg: picture(0, 'self') })
  }
  if (call?.state !== 'active') {
    return
  }
  call.participants.forEach((p, i) => {
    if (!p.self && p.state === 'live' && p.videoOn) {
      emit('meet:frame', { id: p.id, seq: tick, width: 320, height: 240, jpeg: picture(i, p.id) })
    }
  })
}

// Who speaks: the second person in a frozen scene, each person in turn in the live one.
function speakerIndex(): number {
  if (!call || call.state !== 'active') {
    return -1
  }
  const talkers = call.participants.map((p, i) => (p.state === 'live' && p.audioOn ? i : -1)).filter((i) => i >= 0)
  if (talkers.length === 0) {
    return -1
  }
  const forced = query.get('speaker')
  if (forced === 'none') {
    return -1
  }
  if (forced === 'you') {
    return talkers.includes(0) ? 0 : -1
  }
  const preferred = talkers.find((i) => i > 0) ?? -1
  return frozen ? preferred : talkers[Math.floor(tick / 30) % talkers.length]
}

function levels() {
  const list: { id: string; level: number; speaking: boolean }[] = []
  const micOn = (call?.state === 'active' ? call.microphone : lobby.microphone).state === 'on'
  const speaker = speakerIndex()
  if (micOn && !lobby.microphone.hint) {
    const mine = speaker === 0 || !call ? 0.35 + 0.3 * Math.abs(Math.sin(tick / 3)) : 0.04
    list.push({ id: 'self', level: frozen && !call ? 0.62 : mine, speaking: speaker === 0 })
  }
  call?.participants.forEach((p, i) => {
    if (!p.self) {
      list.push({ id: p.id, level: i === speaker ? 0.4 + 0.3 * Math.abs(Math.sin(tick / 3)) : 0, speaking: i === speaker })
    }
  })
  if (list.length > 0) {
    emit('meet:levels', { levels: list })
  }
  if (speaker >= 0 && call && tick % 10 === 0) {
    emit('meet:notch', notchState())
  }
}

window.setInterval(() => {
  pump()
  levels()
}, 100)

function publish() {
  if (call) {
    call.microphone = lobby.microphone
    call.camera = lobby.camera
    call.participants = call.participants.map((p) => (p.self ? { ...p, audioOn: lobby.microphone.state === 'on', videoOn: lobby.camera.state === 'on' } : p))
    call.security = scene === 'call-unknown-mode' ? call.security : security(call)
    call.seq++
    emit('meet:call', { ...call, invite: isNotch ? '' : call.invite })
  }
  emit('meet:notch', notchState())
}

function finish(error: MeetError | null) {
  if (!call) {
    return
  }
  const ended: Call = { ...call, seq: call.seq + 1, state: 'ended', phase: '', finished: Date.now(), error, invite: '' }
  history = [{ id: ended.id, role: ended.role, result: error ? 'failed' : 'done', message: error?.message ?? '', participants: Math.max(1, ended.participants.length), seconds: ended.started ? Math.round((ended.finished - ended.started) / 1000) : 0, at: ended.finished, error }, ...history].slice(0, 50)
  call = ended
  lobby = { microphone: OFF, camera: OFF, speaker: OFF }
  emit('meet:call', ended)
  emit('meet:lobby', lobby)
  emit('meet:history', history)
  emit('meet:notch', notchState())
}

async function runHost(id: string) {
  await pause(900)
  if (call?.id !== id || call.state !== 'creating') {
    return
  }
  call = { ...call, state: 'active', invite: INVITE, started: Date.now(), speaker: ON }
  publish()
  for (let i = 1; i <= 3; i++) {
    await pause(i === 1 ? 6000 : 5000)
    if (call?.id !== id || call.state !== 'active') {
      return
    }
    call.participants = [...call.participants, person(i, { state: 'waiting-keys', audioOn: false, videoOn: false, joined: Date.now() })]
    publish()
    await pause(1100)
    if (call?.id !== id || call.state !== 'active') {
      return
    }
    call.participants = call.participants.map((p) => (p.state === 'waiting-keys' ? { ...p, state: 'live', audioOn: true, videoOn: i !== 2 } : p))
    publish()
  }
}

async function runGuest(id: string) {
  const steps: [Call['state'], string][] = [['joining', 'waiting-for-host'], ['connecting', 'relay']]
  for (const [state, phase] of steps) {
    await pause(1200)
    if (call?.id !== id || call.state === 'ended') {
      return
    }
    call = { ...call, state, phase }
    publish()
  }
  await pause(1200)
  if (call?.id !== id || call.state === 'ended') {
    return
  }
  call = { ...call, state: 'active', phase: '', started: Date.now(), speaker: ON, participants: [call.participants[0], person(1, { host: true }), person(2), person(3, { state: 'waiting-keys', audioOn: false, videoOn: false, joined: Date.now() })] }
  publish()
  await pause(1500)
  if (call?.id === id && call.state === 'active') {
    call.participants = call.participants.map((p) => (p.state === 'waiting-keys' ? { ...p, state: 'live', audioOn: true, videoOn: true } : p))
    publish()
  }
}

function notchState() {
  const empty = { active: false, state: '', role: '', participants: 0, speaker: '', speakerIsYou: false, microphone: false, camera: false, started: 0, hasInvite: false, header: '', message: '', error: null as MeetError | null }
  if (!call || !settings.showInNotch) {
    return empty
  }
  if (call.state === 'ended') {
    return { ...empty, state: 'ended', role: call.role, message: call.error?.message ?? 'You left the call.', error: call.error }
  }
  const speaker = speakerIndex()
  return {
    active: true,
    state: call.state,
    role: call.role,
    participants: call.participants.length,
    speaker: speaker > 0 ? call.participants[speaker].name : '',
    speakerIsYou: speaker === 0,
    microphone: call.microphone.state === 'on',
    camera: call.camera.state === 'on',
    started: call.started,
    hasInvite: call.role === 'host' && call.state === 'active' && call.invite !== '',
    header: call.security.header,
    message: '',
    error: null,
  }
}

export async function Notch() {
  return notchState()
}

export async function NotchLeave() {
  return LeaveCall()
}

// The service copies the invite itself, so the notch page never holds it.
export async function NotchCopyInvite() {
  return CopyInvite()
}

export async function NotchHide() {
  document.body.style.opacity = '0.25'
}

export async function NotchShow() {
  document.body.style.opacity = ''
}

export async function NotchOpenApp() {}

export async function SetShowInNotch(on: boolean) {
  settings = { ...settings, showInNotch: on }
  emit('meet:notch', notchState())
  return settings
}

const info = () => ({ dataDir: settings.dataDir, version: '0.1.0', network })

export async function Start() {
  if (startFails) {
    reject(startFails)
  }
  if (scene === 'starting' && !frozen) {
    window.setTimeout(() => emit('meet:network', { ...network, state: 'ready' }), 2000)
  }
  return info()
}

export async function Info() {
  return info()
}

export async function Settings() {
  return settings
}

export async function Reconnect() {
  network = { ...network, state: 'starting', message: '', error: null }
  emit('meet:network', network)
  window.setTimeout(() => {
    network = { ...network, state: 'ready' }
    emit('meet:network', network)
  }, 1500)
}

export async function SetDisplayName(name: string) {
  const clean = name.replace(/[\p{Cc}\p{Cf}]/gu, '').replace(/\s+/g, ' ').trim()
  if (!clean) {
    reject(meetErr('name-empty'))
  }
  if (Array.from(clean).length > 48) {
    reject(meetErr('name-too-long', '48'))
  }
  settings = { ...settings, displayName: clean }
  if (call) {
    call.participants = call.participants.map((p) => (p.self ? { ...p, name: clean } : p))
    publish()
  }
  return settings
}

export async function Devices() {
  return devices
}

export async function SetDevices(camera: string, microphone: string, speaker: string) {
  const known = (list: { id: string }[], id: string) => id === '' || list.some((d) => d.id === id)
  if (!known(devices.cameras, camera) || !known(devices.microphones, microphone) || !known(devices.speakers, speaker)) {
    reject(meetErr('device-unknown'))
  }
  settings = { ...settings, cameraId: camera, microphoneId: microphone, speakerId: speaker }
  return settings
}

let access = scene === 'lobby-ask' ? 'not-determined' : scene === 'lobby-restricted' ? 'restricted' : 'authorized'
let stopAsking: (() => void) | null = null

export async function MicrophoneAccess() {
  return access
}

// The system prompt: in a frozen scene nobody answers it, so it waits until it is cancelled.
export async function RequestMicrophone() {
  if (access !== 'not-determined') {
    return access
  }
  await new Promise<void>((resolve, fail) => {
    stopAsking = () => fail(Object.assign(new Error('Cancelled.'), { cause: meetErr('cancelled') }))
    if (!frozen) {
      window.setTimeout(resolve, 2500)
    }
  })
  access = 'authorized'
  return access
}

export async function CancelMicrophoneRequest() {
  stopAsking?.()
  stopAsking = null
}

export async function Lobby() {
  return lobby
}

async function setDevice(device: 'microphone' | 'camera', on: boolean) {
  if (on && deny[device]) {
    lobby = { ...lobby, [device]: broken(deny[device]) }
    emit('meet:lobby', lobby)
    publish()
    reject(lobby[device].error!)
  }
  if (on && !frozen) {
    await pause(300)
  }
  lobby = { ...lobby, [device]: on ? { ...ON, deviceId: device === 'camera' ? settings.cameraId : settings.microphoneId } : OFF }
  emit('meet:lobby', lobby)
  publish()
  return lobby[device]
}

export async function SetMicrophone(on: boolean) {
  return setDevice('microphone', on)
}

// The mock draws no pictures, so the size of the tiles changes nothing.
export async function SetTileSize(_width: number, _height: number) {}

// The app writes a page's error to the terminal; the mock writes it to the console.
export async function ReportError(message: string, stack: string) {
  console.error(`frontend error: ${message}\n${stack}`)
}

export async function SetCamera(on: boolean) {
  return setDevice('camera', on)
}

export async function ToggleMicrophone() {
  return setDevice('microphone', lobby.microphone.state !== 'on')
}

export async function ToggleCamera() {
  return setDevice('camera', lobby.camera.state !== 'on')
}

function guard() {
  if (!settings.displayName) {
    reject(meetErr('name-empty'))
  }
  if (call && call.state !== 'ended') {
    reject(meetErr('busy-call'))
  }
  if (network.state !== 'ready') {
    reject(meetErr('network-not-ready'))
  }
}

export async function CreateCall() {
  guard()
  call = makeCall({ role: 'host', state: 'creating', participants: [person(0, { name: settings.displayName })] })
  publish()
  runHost(call.id)
  return call
}

export async function JoinCall(invite: string) {
  if (!invite.replace(/\s+/g, '').startsWith('koloinv0')) {
    reject(meetErr('invalid-invite'))
  }
  guard()
  call = makeCall({ role: 'guest', state: 'joining', phase: 'finding-host', participants: [person(0, { name: settings.displayName })] })
  publish()
  runGuest(call.id)
  return call
}

export async function LeaveCall() {
  if (!call || call.state === 'ended') {
    reject(meetErr('no-call'))
  }
  if (scene === 'call-stuck') {
    reject(meetErr('call-still-ending'))
  }
  await pause(frozen ? 0 : 400)
  finish(null)
}

export async function RemoveParticipant(key: string) {
  if (!call || call.state !== 'active') {
    reject(meetErr('no-call'))
  }
  if (call!.role !== 'host') {
    reject(meetErr('not-host'))
  }
  const p = call!.participants.find((x) => x.id === key)
  if (!p) {
    reject(meetErr('participant-unknown'))
  }
  if (p!.self) {
    reject(meetErr('remove-self'))
  }
  await pause(frozen ? 0 : 300)
  call!.participants = call!.participants.filter((x) => x.id !== key)
  emit('meet:participant', { kind: 'left', id: key, name: p!.name, message: `${p!.name} no longer receives your media.` })
  publish()
}

export async function NewInvite() {
  if (!call || call.state !== 'active') {
    reject(meetErr('no-call'))
  }
  call!.invite = `koloinv0${Array.from({ length: 132 }, () => 'abcdefghijkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789'[Math.floor(Math.random() * 56)]).join('')}`
  return call!.invite
}

export async function Call() {
  return call ? { ...call, invite: isNotch ? '' : call.invite } : null
}

export async function CopyInvite() {
  if (call?.invite) {
    clipboard = call.invite
  }
}

export async function History() {
  return history
}

export async function ClearHistory() {
  history = []
  emit('meet:history', history)
}
