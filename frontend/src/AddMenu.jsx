import { useEffect, useRef, useState } from 'react'
import { useI18n } from './context.js'
import { LinkIcon, PlusIcon, VideoIcon } from './Icons.jsx'
import { menuStep } from './focus.js'

/**
 * The "+" next to Calls. It opens a small menu with the two ways to begin, Start a call and Join with an invite; a row
 * hands its choice and the "+" button to onChoose. The menu closes on Escape and on a click outside, takes the arrow
 * keys, Home, End and Enter, and gives the focus back to the button. With `disabled` it cannot open.
 */
export function AddMenu({ disabled, onChoose }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  const button = useRef(null)
  const menu = useRef(null)

  const items = () => [...(menu.current?.querySelectorAll('[role="menuitem"]:not(:disabled)') ?? [])]
  const close = (refocus) => {
    setOpen(false)
    if (refocus) {
      button.current?.focus()
    }
  }

  useEffect(() => {
    if (!open) {
      return undefined
    }
    ;(items()[0] ?? menu.current)?.focus()
    const away = (ev) => {
      if (!menu.current?.contains(ev.target) && !button.current?.contains(ev.target)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', away)
    return () => document.removeEventListener('mousedown', away)
  }, [open])

  useEffect(() => {
    if (disabled) {
      setOpen(false)
    }
  }, [disabled])

  const onKey = (ev) => {
    if (ev.key === 'Escape') {
      ev.preventDefault()
      ev.stopPropagation()
      close(true)
    } else if (ev.key === 'Tab') {
      close(false)
    } else {
      const list = items()
      const next = menuStep(list.indexOf(document.activeElement), list.length, ev.key)
      if (next >= 0) {
        ev.preventDefault()
        list[next].focus()
      }
    }
  }

  const choose = (kind) => {
    setOpen(false)
    onChoose(kind, button.current)
  }

  return (
    <div className="add">
      <button
        type="button"
        ref={button}
        className="iconbtn iconbtn--box"
        aria-label={t('menu.new')}
        title={t('menu.new')}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={() => setOpen(!open)}
        onKeyDown={(ev) => {
          if (ev.key === 'ArrowDown' && !open) {
            ev.preventDefault()
            setOpen(true)
          }
        }}
      >
        <PlusIcon />
      </button>
      {open ? (
        <div className="popover" role="menu" aria-label={t('menu.new')} ref={menu} tabIndex={-1} onKeyDown={onKey}>
          <button type="button" role="menuitem" className="choice" onClick={() => choose('start')}>
            <span className="choice__icon"><VideoIcon size={20} /></span>
            <span className="choice__text"><span className="choice__title">{t('home.start')}</span><span className="choice__sub">{t('menu.start.lead')}</span></span>
          </button>
          <button type="button" role="menuitem" className="choice" onClick={() => choose('join')}>
            <span className="choice__icon"><LinkIcon size={20} /></span>
            <span className="choice__text"><span className="choice__title">{t('home.join')}</span><span className="choice__sub">{t('menu.join.lead')}</span></span>
          </button>
        </div>
      ) : null}
    </div>
  )
}
