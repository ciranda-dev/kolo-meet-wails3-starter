/** The people of a call, the person itself first; a missing list is an empty one. */
export const peopleOf = (call) => (Array.isArray(call?.participants) ? call.participants : [])

/** Whether this window may remove the person: only the host, in a running call, never itself or the host. */
export const canRemove = (call, person) => call?.role === 'host' && call?.state === 'active' && Boolean(person) && !person.self && !person.host

/** The person whose removal waits for a yes, or null when there is none or they are no longer removable. */
export const asked = (call, id) => (id ? peopleOf(call).find((p) => p.id === id && canRemove(call, p)) ?? null : null)
