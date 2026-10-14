import { describe, expect, it } from 'vitest'
import type { ActivityFeedItem } from '@/api/types'
import {
  emptyFeedFilters, feedFiltersActive, matchesFeedFilters, parseFeedFilters, serializeFeedFilters,
} from '@/lib/feedFilters'

const human = { type: 'user' as const, id: 'u1', name: 'Ana', bot: false }
const agent = { type: 'api_token' as const, id: 'k1', name: 'dev-bot', bot: true }
const item = (over: Partial<ActivityFeedItem> = {}): ActivityFeedItem => ({
  id: 'i1', kind: 'activity', ticket: { id: 't1', ref: 'WEB-30', title: 'CSV' }, actor: human, action: 'updated',
  changes: {}, created_at: '2026-10-01T00:00:00Z', ...over,
})

describe('feed filters url', () => {
  it('parses and ignores unknown values', () => {
    expect(parseFeedFilters({ actor: 'agent', role: 'qa', ticket: ' web-3 ', gates: '1' })).toEqual({
      actor: 'agent', role: 'qa', ticket: 'web-3', gates: true,
    })
    expect(parseFeedFilters({ actor: 'x', role: 'boss', gates: 'yes' })).toEqual(emptyFeedFilters())
  })
  it('round-trips and omits inactive keys', () => {
    expect(serializeFeedFilters(emptyFeedFilters())).toEqual({})
    const f = { actor: 'human' as const, role: 'reviewer' as const, ticket: 'WEB-1', gates: true }
    expect(parseFeedFilters(serializeFeedFilters(f))).toEqual(f)
    expect(feedFiltersActive(f)).toBe(true)
    expect(feedFiltersActive(emptyFeedFilters())).toBe(false)
  })
})

describe('matchesFeedFilters', () => {
  const f = emptyFeedFilters()
  it('everything passes with no filters', () => {
    expect(matchesFeedFilters(item(), f)).toBe(true)
  })
  it('actor kind', () => {
    expect(matchesFeedFilters(item({ actor: agent }), { ...f, actor: 'agent' })).toBe(true)
    expect(matchesFeedFilters(item(), { ...f, actor: 'agent' })).toBe(false)
    expect(matchesFeedFilters(item(), { ...f, actor: 'human' })).toBe(true)
    expect(matchesFeedFilters(item({ actor: agent }), { ...f, actor: 'human' })).toBe(false)
  })
  it('role matches parsed tags on bot comments only', () => {
    const c = item({ kind: 'comment', actor: agent, summary: '[developer] PLAN\nx' })
    expect(matchesFeedFilters(c, { ...f, role: 'developer' })).toBe(true)
    expect(matchesFeedFilters(c, { ...f, role: 'qa' })).toBe(false)
    expect(matchesFeedFilters(item({ kind: 'comment', actor: human, summary: '[developer] PLAN' }), { ...f, role: 'developer' })).toBe(false)
    expect(matchesFeedFilters(item({ actor: agent }), { ...f, role: 'developer' })).toBe(false)
  })
  it('ticket ref is a case-insensitive substring', () => {
    expect(matchesFeedFilters(item(), { ...f, ticket: 'web-30' })).toBe(true)
    expect(matchesFeedFilters(item(), { ...f, ticket: 'WEB-4' })).toBe(false)
  })
  it('gates only: decisions and label moves to or from awaiting-*', () => {
    const g = { ...f, gates: true }
    expect(matchesFeedFilters(item({ kind: 'decision', action: 'decision' }), g)).toBe(true)
    expect(matchesFeedFilters(item({ action: 'decision' }), g)).toBe(true)
    expect(matchesFeedFilters(item({ action: 'labeled', changes: { labels: [['needs-qa'], ['awaiting-merge']] } }), g)).toBe(true)
    expect(matchesFeedFilters(item({ action: 'labeled', changes: { labels: [['awaiting-merge'], []] } }), g)).toBe(true)
    expect(matchesFeedFilters(item({ action: 'labeled', changes: { labels: [['a'], ['a', 'bug']] } }), g)).toBe(false)
    expect(matchesFeedFilters(item({ kind: 'comment', summary: 'hi' }), g)).toBe(false)
  })
})
