import { useEffect, useState } from 'react'

/** The width under which the sidebar is a rail whatever was chosen: the smallest window leaves too little for the page beside it. */
export const NARROW = '(max-width: 700px)'

const matches = (query) => {
  try {
    return window.matchMedia(query).matches
  } catch {
    return false
  }
}

/** Whether the window is narrower than the query, kept current as it is resized. */
export function useNarrow(query = NARROW) {
  const [narrow, setNarrow] = useState(() => matches(query))
  useEffect(() => {
    let list
    try {
      list = window.matchMedia(query)
    } catch {
      return undefined
    }
    const on = () => setNarrow(list.matches)
    on()
    list.addEventListener('change', on)
    return () => list.removeEventListener('change', on)
  }, [query])
  return narrow
}
