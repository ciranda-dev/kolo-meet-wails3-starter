import { useI18n } from './context.js'
import { formatDuration } from './format.js'
import { meetError } from './errors.js'
import { Ring } from './Ring.jsx'
import { Split } from './Split.jsx'
import { Stats } from './parts.jsx'
import { HistoryPane } from './History.jsx'

// A join that failed for one of these can work when asked again with the same invite.
const RETRY = ['call-full', 'host-not-found', 'host-lost', 'host-busy', 'join-rate-limited', 'relay-unreachable', 'lookup-failed', 'network-not-ready', 'host-unreachable', 'host-unreachable-relays', 'relay-no-common']

/** After a call: one sentence that says why it ended, what it was, and the way back, beside the past calls. */
export function Ended({ call, people, canRetry, history, onClear, onHome, onRetry, onNewInvite }) {
  const { t, fail } = useI18n()
  const wasActive = call.started > 0
  const error = call.error
  const reason = meetError(error)?.reason ?? ''
  const title = wasActive || !error ? t('ended.title') : t(call.role === 'host' ? 'ended.title.start' : 'ended.title.join')
  const stats = []
  if (wasActive && call.finished > call.started) {
    stats.push({ label: t('stat.duration'), value: formatDuration(call.finished - call.started) })
  }
  if (wasActive && people > 0) {
    stats.push({ label: t('stat.people'), value: String(people) })
  }
  const guestFailed = !wasActive && error && call.role === 'guest'
  const again = canRetry && RETRY.includes(reason)
  return (
    <Split
      optional
      label={t('history.title')}
      intro={
        <>
          <Ring tone={error ? 'failed' : 'muted'} value={1} />
          <h1 className="intro__title">{title}</h1>
          {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : <p className="intro__note">{t('ended.left')}</p>}
          {stats.length > 0 ? <Stats items={stats} /> : null}
          <div className="hero__actions">
            {guestFailed && again ? <button type="button" className="btn" onClick={onRetry}>{t('ended.retry')}</button> : null}
            {guestFailed && !again ? <button type="button" className="btn" onClick={onNewInvite}>{t('ended.newInvite')}</button> : null}
            <button type="button" className={`btn${guestFailed ? ' btn--ghost' : ''}`} onClick={onHome}>{t('ended.home')}</button>
          </div>
        </>
      }
      side={<HistoryPane items={history} onClear={onClear} />}
    />
  )
}
