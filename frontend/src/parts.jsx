import { useEffect, useRef, useState } from 'react'
import { useI18n } from './context.js'
import { safeText } from './format.js'

/** Label and value columns, mono values, cut with an ellipsis. */
export function Stats({ items }) {
  return (
    <dl className="stats">
      {items.map((item) => (
        <div key={item.label} className="stat">
          <dt className="stat__label">{item.label}</dt>
          <dd className="stat__value" title={typeof item.value === 'string' ? item.value : undefined}>{item.value}</dd>
        </div>
      ))}
    </dl>
  )
}

/** A state as a dot and a sentence; the dot pulses while waiting. */
export function Status({ state, children }) {
  return (
    <p className={`status is-${state}`}>
      <span className="status__dot" aria-hidden="true" />
      <span>{children}</span>
    </p>
  )
}

/** A button that copies and says so for two seconds. `onCopy` does the copying. */
export function CopyButton({ label, onCopy, className = 'btn btn--wide' }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  const timer = useRef(0)
  useEffect(() => () => window.clearTimeout(timer.current), [])
  const copy = () => {
    Promise.resolve(onCopy()).then(() => {
      setCopied(true)
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(() => setCopied(false), 2000)
    }).catch(() => {})
  }
  return (
    <button type="button" className={className} onClick={copy}>
      {copied ? t('invite.copied') : label}
    </button>
  )
}

/** A folder path cut at its start, so the last folders stay readable. */
export function PathText({ value, empty }) {
  if (!value) {
    return <span className="path__value">{empty}</span>
  }
  return (
    <span className="path__value" title={safeText(value)}>
      <bdi>{safeText(value)}</bdi>
    </span>
  )
}

/** The time now, refreshed every `ms` while `running`. */
export function useNow(running, ms = 1000) {
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!running) {
      return undefined
    }
    setNow(Date.now())
    const timer = window.setInterval(() => setNow(Date.now()), ms)
    return () => window.clearInterval(timer)
  }, [running, ms])
  return now
}

/** A device picker: the system default first, then every device by its name. */
export function DeviceSelect({ id, label, list, value, onChange, disabled }) {
  const { t } = useI18n()
  const known = list.some((d) => d.id === value)
  return (
    <div>
      <label className="label" htmlFor={id}>{label}</label>
      <select id={id} className="field" value={value} disabled={disabled} onChange={(ev) => onChange(ev.target.value)}>
        <option value="">{list.length === 0 ? t('device.none') : t('lobby.default')}</option>
        {value && !known ? <option value={value}>{safeText(value)}</option> : null}
        {list.map((d) => (
          <option key={d.id} value={d.id}>{safeText(d.name) || safeText(d.id)}</option>
        ))}
      </select>
    </div>
  )
}
