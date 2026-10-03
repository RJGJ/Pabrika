const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/

/** Parse YYYY-MM-DD as a LOCAL date (never new Date('YYYY-MM-DD'), which is UTC). */
export function parseLocalDate(s: string | null | undefined): Date | null {
  if (typeof s !== 'string') return null
  const m = DATE_RE.exec(s)
  if (!m) return null
  const y = Number(m[1])
  const mo = Number(m[2])
  const d = Number(m[3])
  const date = new Date(y, mo - 1, d)
  if (date.getFullYear() !== y || date.getMonth() !== mo - 1 || date.getDate() !== d) return null
  return date
}

export function toDateInputValue(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** Overdue: due date is before today in the user's local time and the ticket is not done. */
export function isOverdue(due: string | null | undefined, status: string, now: Date = new Date()): boolean {
  if (status === 'done') return false
  const d = parseLocalDate(due)
  if (!d) return false
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate())
  return d.getTime() < today.getTime()
}

export function formatDueDate(due: string | null | undefined): string {
  const d = parseLocalDate(due)
  if (!d) return ''
  return d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

export function relativeTime(iso: string, now: Date = new Date()): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const secs = Math.round((now.getTime() - t) / 1000)
  if (secs < 45) return 'just now'
  const plural = (n: number, unit: string) => `${n} ${unit}${n === 1 ? '' : 's'} ago`
  const mins = Math.round(secs / 60)
  if (mins < 60) return plural(mins, 'minute')
  const hours = Math.round(mins / 60)
  if (hours < 24) return plural(hours, 'hour')
  const days = Math.round(hours / 24)
  if (days < 30) return plural(days, 'day')
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}
