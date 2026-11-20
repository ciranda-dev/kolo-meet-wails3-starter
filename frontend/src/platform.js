/** The system this window runs on, for the words of a permission fix: mac, windows or linux. */
export function detectOS(nav = {}) {
  // The first source that says anything: a browser that hides one still has the next.
  const text = String(nav.userAgentData?.platform || nav.platform || nav.userAgent || '').toLowerCase()
  if (text.includes('mac')) {
    return 'mac'
  }
  if (text.includes('win')) {
    return 'windows'
  }
  return 'linux'
}
