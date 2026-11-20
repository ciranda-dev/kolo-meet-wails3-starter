import { AddMenu } from './AddMenu.jsx'
import { Avatar } from './Avatar.jsx'
import { useI18n } from './context.js'
import { ClockIcon, GearIcon, SidebarIcon, VideoIcon } from './Icons.jsx'
import { Mark } from './Ring.jsx'

/**
 * The sidebar, the full height of the window: the Kolo mark and the button that folds it to a rail (the smallest
 * windows are a rail with no button), the call that runs (a window holds one call, so the list has at most one row)
 * with "+" beside the heading, and a card at the bottom with the name, History, Settings and one quiet line about the
 * network.
 */
export function Sidebar({ entry, name, collapsed, forced, inert, canStart, page, pageButtons, network, onToggle, onChoose, onPage }) {
  const { t } = useI18n()
  const toggle = t(collapsed ? 'sidebar.expand' : 'sidebar.collapse')
  const label = t('rail.call')
  return (
    <nav className={`side${collapsed ? ' is-collapsed' : ''}`} id="calls-panel" aria-label={t('nav.calls')} inert={inert ? '' : undefined}>
      <div className="side__brand">
        <Mark className="side__logo" />
        <span className="side__name">kolo</span>
        <span className="side__app">{t('app.tag')}</span>
        {forced ? null : (
          <button type="button" className="iconbtn" aria-label={toggle} title={toggle} aria-expanded={!collapsed} aria-controls="calls-panel" onClick={onToggle}>
            <SidebarIcon />
          </button>
        )}
      </div>
      <div className="side__heading">
        <h2 className="side__title">{t('nav.calls')}</h2>
        <AddMenu disabled={!canStart} onChoose={onChoose} />
      </div>
      {entry ? null : <p className="side__empty">{t('rail.empty')}</p>}
      <ul className="side__list">
        {entry ? (
          <li>
            <div className="sess is-active" aria-current="page" role="group" aria-label={`${label}, ${entry.text}`} title={`${label}, ${entry.text}`}>
              <span className="sess__avatar"><span className="disc disc--call" aria-hidden="true"><VideoIcon size={22} /></span></span>
              <span className="sess__text">
                <span className="sess__top"><span className="sess__name">{label}</span></span>
                <span className={`sess__line is-${entry.tone}`}>{entry.text}</span>
              </span>
            </div>
          </li>
        ) : null}
      </ul>
      <div className="dock">
        {name ? (
          <p className="dock__who" title={name}>
            <Avatar name={name} seed={name} size={28} />
            <span className="dock__name">{name}</span>
          </p>
        ) : null}
        <div className="dock__nav">
          <button type="button" className="dock__btn" ref={pageButtons.history} aria-haspopup="dialog" aria-label={collapsed ? t('nav.history') : undefined} title={t('nav.history')} onClick={() => onPage('history')}>
            <ClockIcon />
            <span className="dock__label">{t('nav.history')}</span>
          </button>
          <button type="button" className="dock__btn" ref={pageButtons.settings} aria-haspopup="dialog" aria-pressed={page === 'settings'} aria-label={collapsed ? t('nav.settings') : undefined} title={t('nav.settings')} onClick={() => onPage('settings')}>
            <GearIcon />
            <span className="dock__label">{t('nav.settings')}</span>
          </button>
        </div>
        <p className="dock__net" title={network.text}>
          <span className={`dock__dot is-${network.tone}`} aria-hidden="true" />
          <span className="dock__text">{network.text}</span>
        </p>
      </div>
    </nav>
  )
}
