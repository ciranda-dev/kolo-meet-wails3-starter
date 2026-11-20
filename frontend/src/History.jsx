import { useI18n } from './context.js'
import { formatDuration, formatWhen } from './format.js'
import { Ring } from './Ring.jsx'

const ARROW = { host: 'M8 13V3M4 7l4-4 4 4', guest: 'M8 3v10M4 9l4 4 4-4' }

/** Past calls, newest first: one small card per call. It never holds an invite or a name. */
export function HistoryList({ items }) {
  const { t, fail, lang } = useI18n()
  if (items.length === 0) {
    return (
      <div className="history__empty">
        <Ring tone="muted" />
        <p className="history__none">{t('history.empty.title')}</p>
        <p>{t('history.empty')}</p>
      </div>
    )
  }
  return (
    <ul className="history">
      {items.map((item) => {
        const failed = item.result === 'failed'
        const when = formatWhen(item.at, lang)
        const meta = failed ? [t('history.failed')] : [t('history.people', { n: item.participants }), item.seconds > 0 ? formatDuration(item.seconds * 1000) : null]
        const sentence = failed ? fail(item.error, item.message) : ''
        return (
          <li key={item.id}>
            <div className={`item is-${failed ? 'failed' : 'done'}`} title={[sentence, when].filter(Boolean).join('\n')}>
              <span className="item__icon" aria-hidden="true">
                <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
                  <path d={ARROW[item.role] ?? ARROW.guest} />
                </svg>
              </span>
              <span className="item__text">
                <span className="item__name">{t(item.role === 'host' ? 'history.host' : 'history.guest')}</span>
                <span className="item__meta">
                  <span>{meta.filter(Boolean).join(' · ')}</span>
                  <span className="item__when">{when}</span>
                </span>
                {failed ? <span className="item__why">{sentence}</span> : null}
              </span>
            </div>
          </li>
        )
      })}
    </ul>
  )
}

/** The history as a pane of the start screen: its title, the button that empties it, and the calls. */
export function HistoryPane({ items, onClear }) {
  const { t } = useI18n()
  return (
    <>
      <div className="sidebar__head">
        <h2 className="sidebar__title">{t('history.title')}</h2>
        {items.length > 0 ? <button type="button" className="link" aria-label={t('history.clear.label')} onClick={onClear}>{t('history.clear')}</button> : null}
      </div>
      <HistoryList items={items} />
    </>
  )
}
