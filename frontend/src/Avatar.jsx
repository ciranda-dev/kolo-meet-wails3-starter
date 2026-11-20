import { avatarColor } from './avatars.js'
import { initial } from './format.js'

/** A round avatar: the first visible letter of the name on a color of the seed. Without a size the style sheet sizes it. */
export function Avatar({ name, seed, size, className = '' }) {
  const style = { background: avatarColor(seed || name) }
  if (size) {
    Object.assign(style, { width: size, height: size, fontSize: Math.round(size * 0.42) })
  }
  return (
    <span className={`disc ${className}`} style={style} aria-hidden="true">
      {initial(name)}
    </span>
  )
}
