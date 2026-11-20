import { useState } from 'react'
import { useI18n } from './context.js'
import { safeText } from './format.js'
import { known, securityDetail, securityHeader } from './security.js'
import { CopyButton, Stats } from './parts.jsx'

/** Paste an invite, in its dialog. The text stays in this window's memory for the attempt and is never stored. */
export function JoinForm({ value, error, onChange, onPaste, onSubmit }) {
  const { t, fail } = useI18n()
  return (
    <form
      className="joinform"
      onSubmit={(ev) => {
        ev.preventDefault()
        onSubmit()
      }}
    >
      <p className="modal__text" id="join-note">{t('join.note')}</p>
      <label className="label" htmlFor="join-invite">{t('join.label')}</label>
      <textarea
        id="join-invite"
        className="field field--mono joinform__field"
        rows={5}
        value={value}
        onChange={(ev) => onChange(ev.target.value)}
        placeholder={t('join.placeholder')}
        autoComplete="off"
        autoCapitalize="off"
        spellCheck={false}
        aria-invalid={error ? 'true' : undefined}
        aria-describedby="join-note"
      />
      {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : null}
      <div className="modal__actions">
        <button type="button" className="btn btn--ghost" onClick={onPaste}>{t('join.paste')}</button>
        <button type="submit" className="btn">{t('join.continue')}</button>
      </div>
    </form>
  )
}

/**
 * The invite of the host's call: the only place the invite is on screen. Copy invite asks the
 * service to put it on the clipboard; Copy with instructions adds one sentence for the other person.
 */
export function InviteDialog({ invite, onCopy, onCopyMessage, onNew }) {
  const { t, fail } = useI18n()
  const [error, setError] = useState(null)
  const [busy, setBusy] = useState(false)
  const make = () => {
    setBusy(true)
    setError(null)
    onNew().catch(setError).finally(() => setBusy(false))
  }
  return (
    <div className="invite">
      <div>
        <span className="label" id="invite-label">{t('invite.label')}</span>
        <p className="invite__value" tabIndex={0} aria-labelledby="invite-label">{invite}</p>
      </div>
      <div className="invite__actions">
        <CopyButton label={t('invite.copy')} onCopy={onCopy} />
        <CopyButton label={t('invite.copyMessage')} onCopy={onCopyMessage} className="btn btn--ghost btn--wide" />
      </div>
      <p className="note">{t('invite.note')}</p>
      {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : null}
      <div className="invite__more">
        <p className="note">{t('invite.new.note')}</p>
        <button type="button" className="btn btn--ghost btn--small" disabled={busy} onClick={make}>{t('invite.new')}</button>
      </div>
    </div>
  )
}

/**
 * What the line above the grid means: the contract's text, whole, who runs the relay and, for a
 * guest, whether this app reaches the relay directly or through a connection relay.
 */
export function SecurityDialog({ security, link, via }) {
  const { t } = useI18n()
  const named = known(security)
  const stats = [
    { label: t('security.people'), value: String(security.participants) },
    { label: t('security.waiting'), value: String(security.waiting) },
  ]
  if (named) {
    stats.unshift({ label: t('security.operator'), value: security.operatorIsYou ? t('security.operator.you') : safeText(security.operator) })
  }
  const linkText = link === 'direct' ? t('security.via.direct') : link === 'relayed' ? (via ? t('security.via.relayed', { addr: safeText(via) }) : t('call.relayed.unknown')) : ''
  return (
    <div className="security">
      <p className="security__header">{securityHeader(t, security)}</p>
      <ul className="security__list">
        {securityDetail(t, security).map((sentence) => <li key={sentence}>{sentence}</li>)}
      </ul>
      <Stats items={stats} />
      {linkText ? <p className="note">{t('security.via')}: {linkText}</p> : null}
    </div>
  )
}
