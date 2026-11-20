/** Time in a call as a clock: 0:07, 12:04, 1:02:33. */
export function formatDuration(ms) {
  const total = Math.max(0, Math.floor((Number(ms) || 0) / 1000))
  const h = Math.floor(total / 3600)
  const m = Math.floor((total % 3600) / 60)
  const s = String(total % 60).padStart(2, '0')
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`
}

/** A date for the history list: the time for today, the day and month otherwise. */
export function formatWhen(value, lang = 'en', now = new Date()) {
  const date = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(date.getTime())) {
    return ''
  }
  const sameDay = date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth() && date.getDate() === now.getDate()
  const options = sameDay ? { hour: 'numeric', minute: '2-digit' } : { day: 'numeric', month: 'short' }
  return new Intl.DateTimeFormat(lang, options).format(date)
}

/** Turns whatever a rejected call threw into a string for the UI. */
export function errorText(err) {
  if (err instanceof Error && err.message) {
    return err.message
  }
  if (typeof err === 'string') {
    return err
  }
  if (err && typeof err === 'object' && typeof err.message === 'string') {
    return err.message
  }
  return String(err)
}

// Controls, format characters (bidi overrides and isolates, zero-width characters), line and
// paragraph separators, private use and lone surrogates: none of them has a visible shape.
const UNSEEN = /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{Co}\p{Cs}]/gu

/**
 * Text that came from another computer or from the system (a display name, a device name, a path,
 * the words of an error), made safe to show: every character that is invisible or that changes the
 * direction of the text around it is replaced by its code in brackets, so a name cannot pass for
 * another one on screen.
 */
export function safeText(value) {
  return String(value ?? '').replace(UNSEEN, (ch) => `[U+${ch.codePointAt(0).toString(16).toUpperCase().padStart(4, '0')}]`)
}

/** The letter drawn in place of a picture: the first visible character of the safe name. */
export function initial(name) {
  const first = Array.from(safeText(name).trim())[0]
  return first ? first.toLocaleUpperCase() : '?'
}

/** An invite as the service reads it: spaces and line breaks do not count. */
export function cleanInvite(text) {
  return String(text ?? '').replace(/\s+/g, '')
}

/** Whether text can be an invite at all. The service decides the rest. */
export function looksLikeInvite(text) {
  return cleanInvite(text).startsWith('koloinv0')
}

const BASE64 = /^[A-Za-z0-9+/]+={0,2}$/

/** The `src` of a picture from the service: a data URL, or nothing when the payload is not base64. */
export function frameSrc(jpeg) {
  return typeof jpeg === 'string' && jpeg !== '' && BASE64.test(jpeg) ? `data:image/jpeg;base64,${jpeg}` : ''
}
