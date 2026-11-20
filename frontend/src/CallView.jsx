import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { useI18n } from './context.js'
import { formatDuration, safeText } from './format.js'
import { gridLayout } from './layout.js'
import { securityHeader } from './security.js'
import { relayProblems, relaySummary } from './relays.js'
import { keepMedia, useFrames, useSpeaking } from './media.js'
import { useNow } from './parts.jsx'
import { Avatar } from './Avatar.jsx'
import { Ring } from './Ring.jsx'
import { Icon } from './Icons.jsx'
import { isDenied } from './DeviceHelp.jsx'

const GAP = 8
const SLOW_KEYS_MS = 10000

/** One person. Nothing of someone who is waiting for keys is ever shown or played. */
function Tile({ person, width, height, now }) {
  const { t } = useI18n()
  const key = person.self ? 'self' : person.id
  const live = person.state === 'live'
  const video = live && person.videoOn
  const img = useFrames(key, video)
  const speaking = useSpeaking(key, person.speaking) && live && person.audioOn
  const name = safeText(person.name) || t('call.unnamed')
  const shown = person.self ? t('call.you', { name }) : name
  const waiting = person.state === 'waiting-keys'
  const stopped = person.state === 'media-stopped'
  const slow = waiting && person.joined > 0 && now - person.joined > SLOW_KEYS_MS
  const small = width < 220 || height < 150
  const large = width >= 560 && height >= 360
  return (
    <li
      className={`tile${speaking ? ' is-speaking' : ''}${small ? ' is-small' : ''}${large ? ' is-large' : ''}${stopped ? ' is-stopped' : ''}`}
      style={{ width, height }}
      data-pane
      title={slow ? t('call.waitingKeys.long') : undefined}
    >
      {video ? <img ref={img} className={`tile__video${person.self ? ' is-mirrored' : ''}`} alt="" /> : null}
      {waiting ? (
        <>
          <div className="tile__state">
            <Ring busy />
            <p>{t('call.waitingKeys')}</p>
          </div>
          {slow ? <p className="tile__hint">{t('call.waitingKeys.long')}</p> : null}
        </>
      ) : stopped ? (
        <>
          <Avatar className="avatar" name={person.name} seed={person.id} />
          <p className="tile__state">{t('call.mediaStopped')}</p>
        </>
      ) : (
        <Avatar className="avatar" name={person.name} seed={person.id} />
      )}
      <p className="tile__plate" title={shown}>
        {live && !person.audioOn ? <Icon name="mic" off /> : null}
        <span className="tile__name">{shown}</span>
        {person.host ? <span className="tile__tag">{t('call.host')}</span> : null}
        {person.link === 'relayed' ? (
          <span className="tile__tag" title={person.connectionRelay ? t('call.relayed.title', { addr: safeText(person.connectionRelay) }) : t('call.relayed.unknown')}>
            {t('call.relayed')}
          </span>
        ) : null}
        <span className="sr-only">
          {[live && !person.audioOn ? t('call.muted') : '', live && !person.videoOn ? t('call.cameraOff') : '', speaking ? t('call.speaking') : ''].filter(Boolean).map((x) => `, ${x}`).join('')}
        </span>
      </p>
    </li>
  )
}

/** The place of the people who are not here yet, while this person is alone in the call. */
function EmptyTile({ host, width, height, onInvite }) {
  const { t } = useI18n()
  return (
    <li className="tile tile--empty" style={{ width, height }} data-pane>
      <p className="tile__title">{t('call.alone.title')}</p>
      <p className="note">{t(host ? 'call.alone.host' : 'call.alone.guest')}</p>
      {host ? <button type="button" className="btn btn--small" onClick={onInvite}>{t('call.showInvite')}</button> : null}
    </li>
  )
}

/** The grid: every tile the same size, as large as the window lets them be. */
function Grid({ call, onInvite }) {
  const { t } = useI18n()
  const box = useRef(null)
  const [size, setSize] = useState({ width: 0, height: 0 })
  const alone = call.participants.length <= 1
  const count = call.participants.length + (alone ? 1 : 0)
  const waiting = call.participants.some((p) => p.state === 'waiting-keys')
  const now = useNow(waiting, 2000)

  useLayoutEffect(() => {
    const el = box.current
    // Whole pixels, rounded down: a column of a fractional width must not wrap its last tile.
    const measure = () => {
      const box = el.getBoundingClientRect()
      setSize({ width: Math.floor(box.width), height: Math.floor(box.height) })
    }
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  // Pictures and levels are kept for the people of the call only, and for nobody once it is gone.
  const ids = call.participants.map((p) => p.id).join(' ')
  useEffect(() => {
    keepMedia(ids ? ids.split(' ') : [])
  }, [ids])
  useEffect(() => () => keepMedia([]), [])

  const cell = gridLayout(count, size.width, size.height, GAP)
  return (
    <ul className="grid" ref={box} aria-label={t('call.grid')}>
      {cell.width > 0 ? call.participants.map((p, i) => <Tile key={p.id} person={p} width={i >= count - cell.last ? cell.lastWidth : cell.width} height={cell.height} now={now} />) : null}
      {cell.width > 0 && alone ? <EmptyTile host={call.role === 'host'} width={cell.lastWidth} height={cell.height} onInvite={onInvite} /> : null}
    </ul>
  )
}

/** The words on a device button: what pressing it does, or what is wrong with the device. */
function deviceLabel(t, fail, device, media) {
  if (media.state === 'error') {
    return { text: t(`device.${device}`), title: fail(media.error), tone: 'error' }
  }
  const on = media.state === 'on'
  const key = device === 'microphone' ? (on ? 'call.mute' : 'call.unmute') : on ? 'call.stopCamera' : 'call.startCamera'
  return { text: t(key), title: t(`device.${device}.${on ? 'off' : 'on'}`), tone: on ? 'on' : 'off' }
}

/**
 * The call: the line about protection, the grid, and the bar with the time, the microphone, the
 * camera and the way out. People and the invite are in the bar at the top of the window; the invite
 * itself is only in its dialog.
 */
export function CallView({ call, relays, leaving, leaveError, onToggle, onLeave, onInvite, onSecurity, onHelp }) {
  const { t, fail } = useI18n()
  const now = useNow(true)
  const host = call.role === 'host'
  const header = securityHeader(t, call.security)
  const mic = deviceLabel(t, fail, 'microphone', call.microphone)
  const cam = deviceLabel(t, fail, 'camera', call.camera)
  // The microphone is on but only silence leaves it: say so next to its button, with the way to fix it.
  const hint = call.microphone?.state === 'on' ? call.microphone.hint : null
  const problems = [['microphone', call.microphone], ['camera', call.camera], ['speaker', call.speaker]].filter(([, media]) => media?.state === 'error')
  // A warning does not stop the call: a guest whose connection relays share none with the host's.
  const warning = call.warning ?? null
  // Connection relays that hold no place for this call, each with its reason.
  const relayIssues = relayProblems(relays)

  // M and V switch the microphone and the camera, unless the person is typing or a dialog is open.
  useEffect(() => {
    const onKey = (ev) => {
      if (ev.metaKey || ev.ctrlKey || ev.altKey || ev.target.closest?.('input, textarea, select, .modal')) {
        return
      }
      const key = ev.key.toLowerCase()
      if (key === 'm') {
        onToggle('microphone', call.microphone.state !== 'on')
      } else if (key === 'v') {
        onToggle('camera', call.camera.state !== 'on')
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [call.microphone.state, call.camera.state, onToggle])

  return (
    <section className="call">
      {header ? (
        <div className="secbar">
          <Ring peers={Math.max(0, call.participants.length - 1)} />
          <button type="button" className="secbar__line" aria-haspopup="dialog" onClick={onSecurity}>
            {header}
          </button>
          <button type="button" className="pill secbar__more" onClick={onSecurity}>{t('call.security.open')}</button>
        </div>
      ) : null}

      <Grid call={call} onInvite={onInvite} />

      {problems.length > 0 || hint || leaveError || warning || relayIssues.length > 0 ? (
        <div className="call__notices">
          {leaveError ? <div className="notice is-error" role="alert"><p>{fail(leaveError)}</p></div> : null}
          {warning ? <div className="notice is-warning" role="status"><p>{fail(warning)}</p></div> : null}
          {relayIssues.length > 0 ? (
            <div className="notice" role="status">
              <p>
                {relaySummary(t, relays)} {relayIssues.map((s) => fail(s.error)).join(' ')}
              </p>
            </div>
          ) : null}
          {hint ? (
            <div className="notice" role="status">
              <p>{fail(hint)}</p>
              <span className="notice__actions">
                <button type="button" className="btn btn--ghost btn--small" onClick={() => onHelp('microphone')}>{t('help.open')}</button>
              </span>
            </div>
          ) : null}
          {problems.map(([device, media]) => (
            <div key={device} className="notice is-error" role="alert">
              <p>{fail(media.error)}</p>
              {device !== 'speaker' ? (
                <span className="notice__actions">
                  {isDenied(media) ? <button type="button" className="btn btn--ghost btn--small" onClick={() => onHelp(device)}>{t('help.open')}</button> : null}
                  <button type="button" className="btn btn--ghost btn--small" onClick={() => onToggle(device, true)}>{t('help.retry')}</button>
                </span>
              ) : null}
            </div>
          ))}
        </div>
      ) : null}

      <div className="controls" role="group" aria-label={t('call.controls')}>
        <p className="controls__time" role="timer" aria-label={t('call.timer')}>{formatDuration(call.started > 0 ? now - call.started : 0)}</p>
        <div className="controls__main">
          <button type="button" className={`btn btn--device is-${mic.tone}`} title={`${mic.title} (M)`} aria-keyshortcuts="M" onClick={() => onToggle('microphone', call.microphone.state !== 'on')}>
            <Icon name="mic" off={call.microphone.state !== 'on'} />
            <span>{mic.text}</span>
          </button>
          <button type="button" className={`btn btn--device is-${cam.tone}`} title={`${cam.title} (V)`} aria-keyshortcuts="V" onClick={() => onToggle('camera', call.camera.state !== 'on')}>
            <Icon name="camera" off={call.camera.state !== 'on'} />
            <span>{cam.text}</span>
          </button>
        </div>
        <div className="controls__end">
          <button type="button" className="btn btn--danger" disabled={leaving} title={host ? t('call.end.note') : undefined} onClick={onLeave}>
            {leaving ? t('call.leaving') : t(host ? 'call.end' : 'call.leave')}
          </button>
        </div>
      </div>
    </section>
  )
}
