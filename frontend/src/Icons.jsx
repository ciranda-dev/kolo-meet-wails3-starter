const PATHS = {
  mic: ['M8 1.75a2 2 0 0 0-2 2v4a2 2 0 0 0 4 0v-4a2 2 0 0 0-2-2Z', 'M3.75 7.5a4.25 4.25 0 0 0 8.5 0', 'M8 11.75v2.5'],
  camera: ['M1.75 5.25a1.5 1.5 0 0 1 1.5-1.5h5a1.5 1.5 0 0 1 1.5 1.5v5.5a1.5 1.5 0 0 1-1.5 1.5h-5a1.5 1.5 0 0 1-1.5-1.5v-5.5Z', 'm9.75 7 4.5-2.5v7L9.75 9'],
  link: ['M6.75 9.25a3 3 0 0 0 4.24 0l2.13-2.13a3 3 0 0 0-4.24-4.24l-.76.75', 'M9.25 6.75a3 3 0 0 0-4.24 0L2.88 8.88a3 3 0 0 0 4.24 4.24l.76-.75'],
  chevron: ['m3.5 9.5 4.5-4 4.5 4'],
}

/** A 16px line icon. `off` draws a slash through it. */
export function Icon({ name, off = false }) {
  return (
    <svg className="icon" viewBox="0 0 16 16" aria-hidden="true">
      {PATHS[name].map((d) => <path key={d} d={d} />)}
      {off ? <path d="m2.25 2.25 11.5 11.5" /> : null}
    </svg>
  )
}

const line = (children) =>
  function Line({ size = 18 }) {
    return (
      <svg viewBox="0 0 24 24" width={size} height={size} fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
        {children}
      </svg>
    )
  }

export const PlusIcon = line(<path d="M12 5v14M5 12h14" />)
export const GearIcon = line(<><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.9l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.9-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1-1.5 1.7 1.7 0 0 0-1.9.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.9 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1 1.7 1.7 0 0 0-.3-1.9l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.9.3h0a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5h0a1.7 1.7 0 0 0 1.9-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.9v0a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z" /></>)
export const SidebarIcon = line(<><rect x="3" y="4" width="18" height="16" rx="3" /><path d="M9 4v16" /></>)
export const LinkIcon = line(<><path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1" /><path d="M14 10a4 4 0 0 0-5.7 0l-3 3A4 4 0 0 0 11 18.7l1-1" /></>)
export const VideoIcon = line(<><rect x="3" y="6" width="12.5" height="12" rx="3" /><path d="m15.5 10.5 5-2.8v8.6l-5-2.8" /></>)
export const ClockIcon = line(<><circle cx="12" cy="12" r="8.5" /><path d="M12 7.5V12l3 2" /></>)
export const CloseIcon = line(<path d="M6 6l12 12M18 6L6 18" />)
