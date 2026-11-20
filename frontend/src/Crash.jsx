import { Component, useEffect, useRef, useState, useSyncExternalStore } from 'react'
import { copyText } from './service.js'
import { htmlLang, pickLocale, translate } from './i18n.js'
import { loadPrefs } from './prefs.js'
import { crashed, onCrash, running, stopRunning, watchRunning } from './crash.js'
import { errorReport, reportText } from './report.js'

const words = () => {
  const locale = pickLocale(loadPrefs().lang, window.navigator.languages ?? [])
  return { t: (key) => translate(locale, key), lang: htmlLang[locale] }
}

/**
 * The error of a page on screen with a Copy button, never a blank window. The call and the devices keep running in
 * Go, so while a call runs the pane keeps its Leave button, and while the lobby's camera or microphone is on, a
 * button that turns them off. `page` is 'main' or 'notch'; `onDismiss` when the page may go on.
 */
export function CrashPane({ text, page, onDismiss }) {
  const { t, lang } = words()
  const now = useSyncExternalStore(watchRunning, running, running)
  const [copied, setCopied] = useState(false)
  const [busy, setBusy] = useState(false)
  const title = useRef(null)
  const notch = page === 'notch'

  useEffect(() => title.current?.focus(), [])

  const stop = (what) => {
    setBusy(true)
    stopRunning(page, what).catch(() => {}).finally(() => setBusy(false))
  }

  return (
    <div className={`crash${notch ? ' crash--notch' : ''}`} role="alert" lang={lang}>
      <div className="crash__box">
        <h1 className="crash__title" ref={title} tabIndex={-1}>{t('crash.title')}</h1>
        {notch ? null : <p className="crash__note">{t('crash.note')}</p>}
        {now.call || now.devices ? <p className={notch ? 'sr-only' : 'crash__running'}>{t(now.call ? 'crash.call' : 'crash.devices')}</p> : null}
        {notch ? null : <pre className="crash__text">{text}</pre>}
        <div className="crash__actions">
          {now.call ? (
            <button type="button" className="btn btn--danger" disabled={busy} onClick={() => stop('call')}>{t(now.call === 'host' ? 'call.end' : 'call.leave')}</button>
          ) : null}
          {now.devices ? (
            <button type="button" className="btn btn--danger" disabled={busy} onClick={() => stop('devices')}>{t('crash.devicesOff')}</button>
          ) : null}
          <button type="button" className="btn btn--ghost" onClick={() => copyText(text).then(() => setCopied(true)).catch(() => {})}>{t(copied ? 'crash.copied' : 'crash.copy')}</button>
          {onDismiss ? <button type="button" className="btn btn--ghost" onClick={onDismiss}>{t('crash.dismiss')}</button> : null}
        </div>
      </div>
    </div>
  )
}

/** Shows an error that happened outside a render (an event handler, a promise) over the page, which may go on. */
function PageErrors({ page, children }) {
  const [text, setText] = useState('')
  useEffect(() => onCrash(setText), [])
  return (
    <>
      {children}
      {text ? <CrashPane text={text} page={page} onDismiss={() => setText('')} /> : null}
    </>
  )
}

/** The root of a page: a render that throws shows the error pane instead of nothing, and the error goes to the log. */
export class CrashBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { text: '' }
  }

  static getDerivedStateFromError(error) {
    return { text: reportText(errorReport(error)) }
  }

  componentDidCatch(error, info) {
    this.setState({ text: crashed(this.props.page, error, info?.componentStack ?? '') })
  }

  render() {
    if (this.state.text) {
      return <CrashPane text={this.state.text} page={this.props.page} />
    }
    return <PageErrors page={this.props.page}>{this.props.children}</PageErrors>
  }
}
