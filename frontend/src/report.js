import { meetError } from './errors.js'

/** The longest report, message and stack together: the bound of ReportError on the Go side (report.go maxReportRunes). */
export const MAX_REPORT = 8192

// An invite, a 64-character hex key and a data URL (a picture) never leave the window inside an error.
const SECRET = /koloinv0\w*|[0-9a-f]{64,}|data:[^\s"']+/gi

const hide = (text) =>
  text.replace(SECRET, (m) => {
    const low = m.toLowerCase()
    return low.startsWith('koloinv0') ? '[invite]' : low.startsWith('data:') ? '[picture]' : '[key]'
  })

const cut = (text, n) => Array.from(text).slice(0, n).join('')

/**
 * An error as the log and the pane get it: its message and its stack (with the component stack of a render), with
 * invites, keys and pictures hidden, cut to MAX_REPORT characters together. An error of the service is named by its
 * reason and anything else that is not an Error by its type: their words can hold names and addresses.
 */
export function errorReport(error, componentStack = '') {
  const service = meetError(error)
  const plain = error instanceof Error && !service
  const head = plain ? `${error.name}: ${error.message}` : ''
  const message = cut(hide(service ? `MeetError: ${service.reason}` : plain ? head : `Error: a thrown ${error === null ? 'null' : typeof error}`), MAX_REPORT)
  let stack = plain && error.stack ? error.stack : ''
  // Most engines start a stack with the name and the message: say them once.
  if (stack.startsWith(head)) {
    stack = stack.slice(head.length)
  }
  stack = [stack, componentStack].map((s) => s.replace(/^\n+/, '')).filter(Boolean).join('\n')
  return { message, stack: cut(hide(stack), Math.max(0, MAX_REPORT - Array.from(message).length - 1)) }
}

/** The words of a report on the pane and on the clipboard. */
export function reportText(r) {
  return r.stack ? `${r.message}\n${r.stack}` : r.message
}
