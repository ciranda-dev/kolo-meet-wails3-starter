import { useState } from 'react'
import { useI18n } from './context.js'
import { Ring } from './Ring.jsx'
import { Split } from './Split.jsx'
import { HistoryPane } from './History.jsx'

/** First run: calls cannot start until there is a name the others will see. */
export function NameForm({ onSave }) {
  const { t, fail } = useI18n()
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState(null)
  const save = (ev) => {
    ev.preventDefault()
    setBusy(true)
    setError(null)
    onSave(name).catch(setError).finally(() => setBusy(false))
  }
  return (
    <Split
      form
      label={t('name.label')}
      intro={
        <>
          <Ring />
          <h1 className="intro__title">{t('name.title')}</h1>
          <p className="intro__note" id="name-note">{t('name.note')}</p>
        </>
      }
      side={
        <form className="nameform" onSubmit={save}>
          <label className="label" htmlFor="name-field">{t('name.label')}</label>
          <input
            id="name-field"
            className="field"
            value={name}
            onChange={(ev) => {
              setName(ev.target.value)
              setError(null)
            }}
            placeholder={t('name.placeholder')}
            autoComplete="off"
            spellCheck={false}
            autoFocus
            aria-invalid={error ? 'true' : undefined}
            aria-describedby="name-note"
          />
          <button type="submit" className="btn" disabled={busy}>{t('name.save')}</button>
          {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : null}
        </form>
      }
    />
  )
}

/** The start screen: start a call or join one, beside the calls already made. */
export function Home({ max, ready, notice, relays, history, onStart, onJoin, onClear }) {
  const { t } = useI18n()
  return (
    <Split
      optional
      label={t('history.title')}
      intro={
        <>
          <Ring peers={2} />
          <h1 className="intro__title">{t('home.title')}</h1>
          <p className="intro__note">{t('home.note', { n: max })}</p>
          {notice}
          <div className="hero__actions">
            <button type="button" className="btn" disabled={!ready} onClick={onStart}>{t('home.start')}</button>
            <button type="button" className="btn btn--ghost" disabled={!ready} onClick={onJoin}>{t('home.join')}</button>
          </div>
          <p className="intro__hint">{t('home.hint')}</p>
          {relays ? <p className="intro__hint">{relays}</p> : null}
        </>
      }
      side={<HistoryPane items={history} onClear={onClear} />}
    />
  )
}

/** The service did not start: the sentence of the error, and nothing to press. */
export function Fatal({ error }) {
  const { t, fail } = useI18n()
  return (
    <Split
      intro={
        <>
          <Ring tone="failed" value={1} />
          <h1 className="intro__title">{t('fatal.title')}</h1>
          <p className="alert alert--plain" role="alert">{fail(error)}</p>
          <p className="intro__note">{t('fatal.note')}</p>
        </>
      }
    />
  )
}
