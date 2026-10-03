import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { formatDueDate, isOverdue, parseLocalDate, relativeTime, toDateInputValue } from '@/lib/dates'

describe('parseLocalDate', () => {
  it('parses date-only as local midnight, not UTC', () => {
    const d = parseLocalDate('2026-10-15')!
    expect(d.getFullYear()).toBe(2026)
    expect(d.getMonth()).toBe(9)
    expect(d.getDate()).toBe(15)
    expect(d.getHours()).toBe(0)
  })
  it.each(['', 'garbage', '2026-13-01', '2026-02-30', '2026-1-1', null, undefined])('rejects %s', (v) => {
    expect(parseLocalDate(v as string)).toBeNull()
  })
})

describe('isOverdue', () => {
  const now = new Date(2026, 9, 15, 13, 30) // 15 Oct 2026 local
  it('is true before today', () => expect(isOverdue('2026-10-14', 'todo', now)).toBe(true))
  it('is false today', () => expect(isOverdue('2026-10-15', 'todo', now)).toBe(false))
  it('is false in the future', () => expect(isOverdue('2026-10-16', 'todo', now)).toBe(false))
  it('is false when done', () => expect(isOverdue('2026-10-01', 'done', now)).toBe(false))
  it('is false without a date or with a bad date', () => {
    expect(isOverdue(null, 'todo', now)).toBe(false)
    expect(isOverdue('nope', 'todo', now)).toBe(false)
  })
  it('just after local midnight still counts the previous day as overdue', () => {
    expect(isOverdue('2026-10-14', 'backlog', new Date(2026, 9, 15, 0, 0, 1))).toBe(true)
  })
})

describe('formatDueDate / toDateInputValue', () => {
  it('formats a local date', () => {
    expect(formatDueDate('2026-10-15')).toMatch(/15/)
    expect(formatDueDate(null)).toBe('')
  })
  it('round-trips to the input value', () => {
    expect(toDateInputValue(new Date(2026, 0, 5))).toBe('2026-01-05')
  })
})

describe('relativeTime', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())
  const now = new Date('2026-10-03T12:00:00Z')
  it.each([
    ['2026-10-03T11:59:40Z', 'just now'],
    ['2026-10-03T11:55:00Z', '5 minutes ago'],
    ['2026-10-03T11:00:00Z', '1 hour ago'],
    ['2026-10-01T12:00:00Z', '2 days ago'],
  ])('%s -> %s', (iso, expected) => {
    expect(relativeTime(iso, now)).toBe(expected)
  })
  it('falls back to a date for old timestamps', () => {
    expect(relativeTime('2025-01-01T00:00:00Z', now)).toMatch(/2025/)
  })
  it('handles invalid input', () => {
    expect(relativeTime('bad', now)).toBe('')
  })
})
