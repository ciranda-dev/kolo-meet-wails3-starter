import { useI18n } from './context.js'
import { Ring } from './Ring.jsx'
import { Split } from './Split.jsx'
import { Status } from './parts.jsx'
import { relayProblems } from './relays.js'

// What a guest goes through, in the order of the contract's state machine.
const GUEST_STEPS = ['finding-host', 'waiting-for-host', 'relay']

/** The sentence for where a call that is not active yet stands. */
export function phaseText(t, call) {
  if (call.state === 'creating') {
    return t('phase.creating')
  }
  if (GUEST_STEPS.includes(call.phase)) {
    return t(`phase.${call.phase}`)
  }
  return t(call.state === 'connecting' ? 'phase.connecting' : 'phase.joining')
}

/** Starting or joining: the ring sweeps, one sentence says what is being reached, Cancel leaves. */
export function Joining({ call, relays, leaving, leaveError, onCancel }) {
  const { t, fail } = useI18n()
  // A relay with a wrong key is known at start: a guest hears of it now, not when the join times out.
  const relayIssues = relayProblems(relays)
  const text = phaseText(t, call)
  const host = call.role === 'host'
  const now = GUEST_STEPS.indexOf(call.phase)
  return (
    <Split
      form
      label={t('joining.progress')}
      intro={
        <>
          <Ring busy label={t('joining.progress')} valueText={text} />
          <h1 className="intro__title">{t(host ? 'lobby.title.start' : 'lobby.title.join')}</h1>
          <Status state="waiting">{text}</Status>
          <p className="intro__note">{t(host ? 'joining.note.host' : 'joining.note.guest')}</p>
          {!host && (relays ?? []).length > 0 ? <p className="intro__note">{t('joining.note.relays')}</p> : null}
          {relayIssues.length > 0 ? <p className="alert alert--plain" role="status">{relayIssues.map((s) => fail(s.error)).join(' ')}</p> : null}
          {leaveError ? <p className="alert alert--plain" role="alert">{fail(leaveError)}</p> : null}
          <div className="hero__actions">
            <button type="button" className="btn btn--ghost" disabled={leaving} onClick={onCancel}>{t('joining.cancel')}</button>
          </div>
        </>
      }
      side={
        host || now < 0 ? null : (
          <ol className="steps">
            {GUEST_STEPS.map((phase, i) => (
              <li key={phase} className={`step${i < now ? ' is-done' : i === now ? ' is-now' : ''}`} aria-current={i === now ? 'step' : undefined}>
                <span className="step__mark" aria-hidden="true">{i + 1}</span>
                <span>{t(`phase.${phase}`)}</span>
              </li>
            ))}
          </ol>
        )
      }
    />
  )
}
