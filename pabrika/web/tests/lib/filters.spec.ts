import { describe, expect, it } from 'vitest'
import type { TicketSummary } from '@/api/types'
import summary from '../fixtures/ticket-summary.json'
import { emptyFilters, filtersActive, matchesFilters, parseFilters, serializeFilters } from '@/lib/filters'

const t = (over: Partial<TicketSummary> = {}): TicketSummary => ({ ...(summary as TicketSummary), ...over })
const known = {
  members: [{ user: { id: 'u1', email: 'a@x', display_name: 'A' } }],
  labels: [{ id: 'l1' }, { id: 'l2' }],
}

describe('parseFilters', () => {
  it('reads all keys', () => {
    const f = parseFilters({ q: ' fix ', priority: 'high,urgent', assignee: 'u1', label: 'l1,l2' }, known)
    expect(f).toEqual({ q: 'fix', priority: ['high', 'urgent'], assignee: 'u1', label: ['l1', 'l2'] })
  })
  it('ignores unknown values', () => {
    const f = parseFilters({ priority: 'high,bogus', assignee: 'ghost', label: 'l1,nope' }, known)
    expect(f.priority).toEqual(['high'])
    expect(f.assignee).toBeNull()
    expect(f.label).toEqual(['l1'])
  })
  it('keeps me and none, and ids when members are not loaded yet', () => {
    expect(parseFilters({ assignee: 'me' }, known).assignee).toBe('me')
    expect(parseFilters({ assignee: 'none' }, known).assignee).toBe('none')
    expect(parseFilters({ assignee: 'zzz', label: 'q' }).assignee).toBe('zzz')
  })
  it('round-trips through serializeFilters and omits inactive keys', () => {
    expect(serializeFilters(emptyFilters())).toEqual({})
    const f = { q: 'a', priority: ['low' as const], assignee: 'me', label: ['l1'] }
    expect(parseFilters(serializeFilters(f), known)).toEqual(f)
  })
})

describe('matchesFilters', () => {
  it('searches ref and title case-insensitively', () => {
    expect(matchesFilters(t(), { ...emptyFilters(), q: 'DARK' }, null)).toBe(true)
    expect(matchesFilters(t(), { ...emptyFilters(), q: 'web-13' }, null)).toBe(true)
    expect(matchesFilters(t(), { ...emptyFilters(), q: 'zzz' }, null)).toBe(false)
  })
  it('filters by priority (any of)', () => {
    expect(matchesFilters(t({ priority: 'high' }), { ...emptyFilters(), priority: ['high', 'urgent'] }, null)).toBe(true)
    expect(matchesFilters(t({ priority: 'low' }), { ...emptyFilters(), priority: ['high'] }, null)).toBe(false)
  })
  it('filters by assignee me, none and a member', () => {
    const a = { id: 'u1', display_name: 'A', email: 'a@x' }
    expect(matchesFilters(t({ assignee: a }), { ...emptyFilters(), assignee: 'me' }, 'u1')).toBe(true)
    expect(matchesFilters(t({ assignee: a }), { ...emptyFilters(), assignee: 'me' }, 'u2')).toBe(false)
    expect(matchesFilters(t(), { ...emptyFilters(), assignee: 'none' }, 'u1')).toBe(true)
    expect(matchesFilters(t({ assignee: a }), { ...emptyFilters(), assignee: 'none' }, 'u1')).toBe(false)
    expect(matchesFilters(t({ assignee: a }), { ...emptyFilters(), assignee: 'u1' }, null)).toBe(true)
  })
  it('filters by label (any of) and combines with AND', () => {
    const lab = { id: 'l1', project_id: 'p', name: 'bug', color: 'red' }
    expect(matchesFilters(t({ labels: [lab] }), { ...emptyFilters(), label: ['l1', 'l9'] }, null)).toBe(true)
    expect(matchesFilters(t(), { ...emptyFilters(), label: ['l1'] }, null)).toBe(false)
    expect(matchesFilters(t({ labels: [lab], priority: 'low' }), { ...emptyFilters(), label: ['l1'], priority: ['high'] }, null)).toBe(false)
  })
  it('filtersActive', () => {
    expect(filtersActive(emptyFilters())).toBe(false)
    expect(filtersActive({ ...emptyFilters(), q: 'x' })).toBe(true)
  })
})
