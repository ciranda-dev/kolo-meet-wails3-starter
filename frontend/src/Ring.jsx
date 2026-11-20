const R = 10
const C = 2 * Math.PI * R
/** Where the other people sit on the ring, in degrees clockwise from the top. The first two are the dots of the Kolo mark. */
const SEATS = [106, 216, 290, 160, 50, 254, 328]

/**
 * The Kolo ring as the picture of a call. The accent dot at the top is this person and every other
 * dot is someone else in the call. `busy` sweeps an arc while something is being reached; `value`
 * (0 to 1) fills it. Nothing is drawn in the middle.
 */
export function Ring({ value = 0, busy = false, peers = 0, tone = '', label, valueText }) {
  const shown = Math.min(Math.max(Number(value) || 0, 0), 1)
  const seats = SEATS.slice(0, Math.min(Math.max(peers, 0), SEATS.length))
  const arc = busy ? { strokeDasharray: `${C / 4} ${(C * 3) / 4}` } : { strokeDasharray: C, strokeDashoffset: C * (1 - shown) }
  return (
    <div
      className="ringbox"
      role={busy ? 'progressbar' : 'img'}
      aria-label={label}
      aria-valuetext={busy ? valueText : undefined}
      aria-hidden={label ? undefined : 'true'}
    >
      <svg className={`ring${busy ? ' is-busy' : ''}${tone ? ` is-${tone}` : ''}`} viewBox="0 0 26 26" aria-hidden="true">
        <circle className="ring__track" cx="13" cy="13" r={R} />
        {busy || shown > 0 ? <circle className="ring__arc" cx="13" cy="13" r={R} style={arc} /> : null}
        {seats.map((deg) => {
          const rad = (deg * Math.PI) / 180
          return <circle key={deg} className="ring__peer" cx={13 + R * Math.sin(rad)} cy={13 - R * Math.cos(rad)} r="2.4" />
        })}
        <circle className="ring__me" cx="13" cy="3" r="2.4" />
      </svg>
    </div>
  )
}

/** The Kolo mark, drawn in the current text color with the accent dot on top. */
export function Mark({ className }) {
  return (
    <svg className={className} viewBox="0 0 26 26" aria-hidden="true">
      <circle cx="13" cy="13" r="10" fill="none" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="13" cy="3" r="2.4" fill="var(--accent)" />
      <circle cx="22.7" cy="15.8" r="2.4" fill="currentColor" />
      <circle cx="7" cy="21.2" r="2.4" fill="currentColor" />
    </svg>
  )
}
