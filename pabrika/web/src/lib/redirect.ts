/** Only same-origin relative paths are allowed after login; anything else becomes "/". */
export function safeRedirect(value: unknown): string {
  if (typeof value !== 'string' || value === '') return '/'
  if (!value.startsWith('/') || value.startsWith('//')) return '/'
  if (value.includes('\\')) return '/'
  // Browsers strip tabs/newlines inside URLs ("/\t/evil" becomes "//evil"); reject control characters.
  for (let i = 0; i < value.length; i++) if (value.charCodeAt(i) < 0x20 || value.charCodeAt(i) === 0x7f) return '/'
  const path = value.split(/[?#]/)[0]
  if (path === '/login' || path === '/signup') return '/'
  return value
}
