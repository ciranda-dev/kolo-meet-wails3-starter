import { useEffect, useRef, useState } from 'react'
import { call, on } from './service.js'
import { frameSrc } from './format.js'
import { createMediaStore } from './mediastore.js'
import { largestTile } from './tilesize.js'

// Pictures and loudness arrive many times a second, so they do not go through React state: a
// picture replaces the `src` of the one `img` of its tile, and a level moves a meter directly.
const store = createMediaStore()
let started = false

function start() {
  if (started) {
    return
  }
  started = true
  on('meet:frame', (frame) => {
    const src = frame ? frameSrc(frame.jpeg) : ''
    if (src) {
      store.frame(frame.id, src).forEach((img) => show(img, src))
    }
  })
  on('meet:levels', (event) => {
    for (const item of event?.levels ?? []) {
      const next = { level: Math.min(Math.max(Number(item.level) || 0, 0), 1), speaking: Boolean(item.speaking) }
      store.level(item.id, next).forEach((cb) => cb(next))
    }
  })
}

// The size of every picture on screen: the Go side draws pictures as large as the largest is shown.
const sizes = new Map()
let told = ''
let observer = null

function tell() {
  if (sizes.size === 0) {
    return
  }
  const { width, height } = largestTile(sizes.values(), window.devicePixelRatio)
  const key = `${width}x${height}`
  if (key !== told) {
    told = key
    call('SetTileSize', width, height).catch(() => {})
  }
}

// A move to a screen of another pixel ratio changes the device pixels without a resize.
function watchRatio() {
  window.matchMedia?.(`(resolution: ${window.devicePixelRatio}dppx)`)?.addEventListener('change', () => {
    watchRatio()
    tell()
  }, { once: true })
}

function measure(img) {
  if (!observer) {
    observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        // A size reported after the picture went away is ignored.
        if (sizes.has(entry.target)) {
          sizes.set(entry.target, { width: entry.contentRect.width, height: entry.contentRect.height })
        }
      }
      tell()
    })
    watchRatio()
  }
  sizes.set(img, { width: 0, height: 0 })
  observer.observe(img)
  return () => {
    observer.unobserve(img)
    sizes.delete(img)
  }
}

function show(img, src) {
  img.src = src
  img.dataset.live = '1'
}

function clear(img) {
  img.removeAttribute('src')
  delete img.dataset.live
}

/** Keeps pictures and levels for these participant IDs only; everyone else is forgotten. */
export function keepMedia(ids) {
  store.keep(ids)
}

/**
 * The `ref` of the one `img` that shows the pictures of `id` (a participant ID, or "self"). While
 * `enabled` is false nothing is shown and the last picture is forgotten.
 */
export function useFrames(id, enabled) {
  const ref = useRef(null)
  useEffect(() => {
    start()
    const img = ref.current
    if (!enabled || !img) {
      store.drop(id).forEach(clear)
      return undefined
    }
    const off = store.show(id, img)
    const unmeasure = measure(img)
    const src = store.picture(id)
    if (src) {
      show(img, src)
    }
    return () => {
      off()
      unmeasure()
      clear(img)
    }
  }, [id, enabled])
  return ref
}

/** Whether `id` is speaking now. Starts from the snapshot of the call and then follows meet:levels. */
export function useSpeaking(id, snapshot = false) {
  const [speaking, setSpeaking] = useState(() => store.levelOf(id)?.speaking ?? snapshot)
  useEffect(() => {
    start()
    return store.watch(id, (next) => setSpeaking(next.speaking))
  }, [id])
  return speaking
}

/** The `ref` of a meter bar that follows the loudness of `id`, from empty to full. */
export function useLevelBar(id, enabled) {
  const ref = useRef(null)
  useEffect(() => {
    const bar = ref.current
    if (!bar) {
      return undefined
    }
    start()
    const set = (level) => {
      bar.style.transform = `scaleX(${enabled ? level : 0})`
    }
    set(store.levelOf(id)?.level ?? 0)
    return store.watch(id, (next) => set(next.level))
  }, [id, enabled])
  return ref
}
