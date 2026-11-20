import { useCallback, useEffect, useRef, useState } from 'react'
import { call, on } from './service.js'
import { htmlLang, pickLocale, translate } from './i18n.js'
import { formatDuration, safeText } from './format.js'
import { errorSentence } from './errors.js'
import { loadPrefs } from './prefs.js'
import { useNow } from './parts.jsx'
import { Ring } from './Ring.jsx'
import { Icon } from './Icons.jsx'
import './meet.css'
import './notch.css'

const PENDING = ['creating', 'joining', 'connecting']

/** The first line: who is speaking in a call, else where the call stands. */
function title(t, s) {
  if (PENDING.includes(s.state)) {
    return t(`notch.state.${s.state}`)
  }
  if (!s.active) {
    return t('notch.state.ended')
  }
  if (s.speakerIsYou) {
    return t('notch.speaking.you')
  }
  const speaker = safeText(s.speaker)
  if (speaker) {
    return t('notch.speaking', { name: speaker })
  }
  return t(s.participants <= 1 ? 'notch.alone' : 'notch.quiet')
}

/**
 * The page of the macOS notch window: the call bar, in one row. The library draws the notch; this
 * page only fills its 660 x 92 area and shows nothing when there is no call. It gets the number of
 * people, who is speaking and the state of the two devices. It never holds the invite: the Copy
 * button asks the service to put it on the clipboard.
 */
export function Notch() {
  const [locale, setLocale] = useState(() => pickLocale(loadPrefs().lang, window.navigator.languages ?? []))
  const [state, setState] = useState(null)
  const [copied, setCopied] = useState(false)
  const timer = useRef(0)
  const lang = htmlLang[locale]
  const t = useCallback((key, vars) => translate(locale, key, vars), [locale])
  const active = Boolean(state?.active) && state.state === 'active'
  const now = useNow(active)

  useEffect(() => {
    document.documentElement.lang = lang
  }, [lang])

  useEffect(() => {
    const off = on('meet:notch', (next) => next && setState(next))
    call('Notch').then((now) => setState((prev) => prev ?? now)).catch(() => {})
    // The main window saves the language; follow it without a reload.
    const onStorage = () => setLocale(pickLocale(loadPrefs().lang, window.navigator.languages ?? []))
    window.addEventListener('storage', onStorage)
    return () => {
      off()
      window.removeEventListener('storage', onStorage)
      window.clearTimeout(timer.current)
    }
  }, [])

  if (!state || (!state.state && !state.message && !state.error)) {
    return <main className="notch" aria-label={t('app.title')} />
  }

  const pending = PENDING.includes(state.state)
  const over = !state.active
  const failed = over && Boolean(state.error)
  const sentence = over ? errorSentence(t, locale, state.error, state.error ? '' : t('ended.left')) : ''
  const copy = () => {
    call('NotchCopyInvite').then(() => {
      setCopied(true)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setCopied(false), 2000)
    }).catch(() => {})
  }

  return (
    <main className="notch" aria-label={t('app.title')}>
      <Ring
        busy={pending}
        peers={active ? Math.max(0, state.participants - 1) : 0}
        tone={failed ? 'failed' : over ? 'muted' : ''}
        value={over ? 1 : 0}
        label={t('notch.ring')}
        valueText={pending ? title(t, state) : undefined}
      />
      <div className="notch__text">
        {failed ? null : <p className="notch__name" title={title(t, state)}>{title(t, state)}</p>}
        {over ? (
          <p className={`notch__detail is-sentence${failed ? ' is-failed' : ''}`} role="status" title={sentence}>{sentence}</p>
        ) : (
          <p className="notch__detail" role="status">{copied ? t('notch.copied') : active ? t('notch.people', { n: state.participants }) : ''}</p>
        )}
      </div>
      {active ? <p className="notch__datum" role="timer" aria-label={t('call.timer')}>{formatDuration(state.started > 0 ? now - state.started : 0)}</p> : null}
      {active ? (
        <>
          <button
            type="button"
            className={`notch__round${state.microphone ? ' is-on' : ''}`}
            aria-label={t(state.microphone ? 'device.microphone.off' : 'device.microphone.on')}
            title={t(state.microphone ? 'device.microphone.off' : 'device.microphone.on')}
            onClick={() => call('ToggleMicrophone').catch(() => {})}
          >
            <Icon name="mic" off={!state.microphone} />
          </button>
          <button
            type="button"
            className={`notch__round${state.camera ? ' is-on' : ''}`}
            aria-label={t(state.camera ? 'device.camera.off' : 'device.camera.on')}
            title={t(state.camera ? 'device.camera.off' : 'device.camera.on')}
            onClick={() => call('ToggleCamera').catch(() => {})}
          >
            <Icon name="camera" off={!state.camera} />
          </button>
          {state.hasInvite ? (
            <button type="button" className="notch__round" aria-label={t('notch.copyInvite')} title={t('notch.copyInvite')} onClick={copy}>
              <Icon name="link" />
            </button>
          ) : null}
        </>
      ) : null}
      {over ? (
        <button type="button" className="btn btn--ghost notch__action" onClick={() => call('NotchOpenApp').catch(() => {})}>{t('notch.open')}</button>
      ) : (
        <button type="button" className="btn btn--danger notch__action" onClick={() => call('NotchLeave').catch(() => {})}>{t(pending ? 'joining.cancel' : 'notch.leave')}</button>
      )}
      <button type="button" className="notch__round notch__hide" aria-label={t('notch.hide')} title={t('notch.hide')} onClick={() => call('NotchHide').catch(() => {})}>
        <Icon name="chevron" />
      </button>
    </main>
  )
}
