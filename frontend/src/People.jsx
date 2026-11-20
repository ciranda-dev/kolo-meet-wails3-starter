import { useI18n } from './context.js'
import { Avatar } from './Avatar.jsx'
import { safeText } from './format.js'
import { asked, canRemove, peopleOf } from './roster.js'

/** Who is in the call. The host can remove anyone but itself, and is asked first. */
export function PeopleList({ call, asking, removing, error, onAsk, onConfirm, onCancel }) {
  const { t, fail } = useI18n()
  const host = call?.role === 'host'
  const waiting = asked(call, asking)
  return (
    <div className="people">
      {host ? <p className="people__note">{t('people.note')}</p> : null}
      <ul className="people__list">
        {peopleOf(call).map((p) => {
          const name = safeText(p.name) || t('call.unnamed')
          const open = waiting?.id === p.id
          return (
            <li key={p.id} className={`people__row${open ? ' is-asking' : ''}`}>
              <Avatar className="avatar" name={p.name} seed={p.id} />
              <span className="people__name">{p.self ? t('call.you', { name }) : name}</span>
              {p.host ? <span className="tile__tag">{t('call.host')}</span> : null}
              {canRemove(call, p) && !open ? (
                <button type="button" className="btn btn--ghost btn--small" onClick={() => onAsk(p.id)}>{t('people.remove')}</button>
              ) : null}
              {open ? (
                <div className="people__ask" role="group" aria-label={t('people.remove.ask', { name })}>
                  <p>{t('people.remove.ask', { name })}</p>
                  <span className="notice__actions">
                    <button type="button" className="btn btn--ghost btn--small" onClick={onCancel}>{t('people.remove.cancel')}</button>
                    <button type="button" className="btn btn--danger btn--small" disabled={removing} onClick={() => onConfirm(p.id)}>
                      {removing ? t('people.removing') : t('people.remove.yes')}
                    </button>
                  </span>
                </div>
              ) : null}
            </li>
          )
        })}
      </ul>
      {error ? <p className="alert alert--plain" role="alert">{fail(error)}</p> : null}
    </div>
  )
}
