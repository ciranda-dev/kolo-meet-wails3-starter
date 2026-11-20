/** The JSON fields that hold a list in any payload of the service: the list fields of the Go types. */
export const LIST_FIELDS = ['participants', 'relays', 'levels', 'cameras', 'microphones', 'speakers']

/** The events and methods whose payload is itself a list. */
export const LIST_PAYLOADS = ['meet:history', 'History']

function fix(value, keep = []) {
  if (Array.isArray(value)) {
    value.forEach((item) => fix(item, keep))
  } else if (value && typeof value === 'object') {
    for (const key of Object.keys(value)) {
      if (LIST_FIELDS.includes(key) && !keep.includes(key) && !Array.isArray(value[key])) {
        value[key] = []
      }
      fix(value[key], [])
    }
  }
  return value
}

/**
 * A payload of an event or a method with every list a list. Go sends null for a list it never made, and the window
 * reads lists without a check, so every payload goes through here as it arrives (service.js). It changes the payload
 * in place: each one is a new object.
 */
export function withLists(name, payload) {
  if (LIST_PAYLOADS.includes(name)) {
    // A past call counts its people: `participants` is a number there, and stays one.
    return Array.isArray(payload) ? fix(payload, ['participants']) : []
  }
  return fix(payload)
}
