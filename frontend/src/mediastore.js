/**
 * What the window keeps per person between events: the newest picture, the newest level, and who is
 * showing them. Only the people of the call (and "self") are kept: anything for another ID is
 * dropped on arrival, and `keep` forgets everyone who is no longer in the call.
 */
export function createMediaStore() {
  const latest = new Map()
  const levels = new Map()
  const images = new Map()
  const listeners = new Map()
  let allowed = new Set(['self'])

  const add = (map, id, item) => {
    if (!map.has(id)) {
      map.set(id, new Set())
    }
    map.get(id).add(item)
    return () => {
      const set = map.get(id)
      set?.delete(item)
      if (set?.size === 0) {
        map.delete(id)
      }
    }
  }

  return {
    /** The people to keep state for; "self" always is. Everyone else is forgotten now. */
    keep(ids) {
      allowed = new Set(['self', ...ids])
      for (const map of [latest, levels]) {
        for (const id of map.keys()) {
          if (!allowed.has(id)) {
            map.delete(id)
          }
        }
      }
    },
    frame(id, src) {
      if (!allowed.has(id)) {
        return []
      }
      latest.set(id, src)
      return [...(images.get(id) ?? [])]
    },
    level(id, value) {
      if (!allowed.has(id)) {
        return []
      }
      levels.set(id, value)
      return [...(listeners.get(id) ?? [])]
    },
    /** Forgets the pictures of one person and returns the images that were showing them. */
    drop(id) {
      latest.delete(id)
      return [...(images.get(id) ?? [])]
    },
    picture: (id) => latest.get(id),
    levelOf: (id) => levels.get(id),
    show: (id, img) => add(images, id, img),
    watch: (id, cb) => add(listeners, id, cb),
    size: () => latest.size + levels.size + images.size + listeners.size,
  }
}
