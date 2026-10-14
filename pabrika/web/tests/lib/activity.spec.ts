import { describe, expect, it } from 'vitest'
import { humanizeActivity, type ActivityLike } from '@/lib/activity'
import type { Activity, Member } from '@/api/types'

const members: Member[] = [
  { user: { id: 'u1', email: 'a@x.co', display_name: 'Ada' }, role: 'owner', created_at: '' },
  { user: { id: 'u2', email: 'g@x.co', display_name: 'Grace' }, role: 'editor', created_at: '' },
]

function act(action: string, changes: Record<string, unknown> = {}): Activity {
  return {
    id: 'a', ticket_id: 't', action, changes, created_at: '',
    actor: { type: 'user', id: 'u1', name: 'Ada', bot: false },
  }
}
const h = (a: ActivityLike) => humanizeActivity(a, members)

describe('humanizeActivity', () => {
  it('created', () => expect(h(act('created'))).toBe('created the ticket'))
  it('deleted', () => expect(h(act('deleted'))).toBe('deleted the ticket'))
  it('moved shows readable status names', () => {
    expect(h(act('moved', { status: ['todo', 'in_progress'] }))).toBe('moved from To do to In progress')
  })
  it('moved with only position is reordered and never shows the number', () => {
    const t = h(act('moved', { position: [1024, 2048] }))
    expect(t).toBe('reordered the ticket')
    expect(t).not.toMatch(/\d/)
  })
  it('position is hidden from mixed changes', () => {
    const t = h(act('moved', { status: ['todo', 'done'], position: [1, 2] }))
    expect(t).toBe('moved from To do to Done')
  })
  it('assigned resolves ids through members', () => {
    expect(h(act('assigned', { assignee: [null, 'u2'] }))).toBe('assigned to Grace')
    expect(h(act('assigned', { assignee: ['u2', null] }))).toBe('unassigned Grace')
    expect(h(act('assigned', { assignee: ['u1', 'u2'] }))).toBe('reassigned from Ada to Grace')
  })
  it('unknown assignee id falls back to "someone"', () => {
    expect(h(act('assigned', { assignee: [null, 'gone'] }))).toBe('assigned to someone')
  })
  it('labeled uses name lists', () => {
    expect(h(act('labeled', { labels: [['bug'], ['bug', 'ui']] }))).toBe('added label ui')
    expect(h(act('labeled', { labels: [['bug', 'ui'], ['bug']] }))).toBe('removed label ui')
    expect(h(act('labeled', { labels: [[], ['a', 'b']] }))).toBe('added labels a, b')
  })
  it('updated priority, title, due date', () => {
    expect(h(act('updated', { priority: ['low', 'urgent'] }))).toBe('changed priority from low to urgent')
    expect(h(act('updated', { title: ['Old', 'New'] }))).toBe('renamed the ticket from "Old" to "New"')
    expect(h(act('updated', { due_date: [null, '2026-10-15'] }))).toBe('set the due date to 2026-10-15')
    expect(h(act('updated', { due_date: ['2026-10-15', null] }))).toBe('cleared the due date')
  })
  it('description never shows the text', () => {
    const t = h(act('updated', { description: ['secret old', 'secret new'] }))
    expect(t).toBe('changed the description')
    expect(t).not.toContain('secret')
  })
  it('combines several fields', () => {
    expect(h(act('updated', { priority: ['low', 'high'], description: ['a', 'b'] }))).toBe(
      'changed priority from low to high and changed the description',
    )
  })
  it('unknown field falls back to a generic line', () => {
    expect(h(act('updated', { weird: [1, 2] }))).toBe('updated weird')
  })
  it('unknown action falls back', () => {
    expect(h(act('exploded'))).toBe('updated the ticket')
    expect(h(act('exploded', { weird: [1, 2] }))).toBe('updated weird')
  })
  it('tolerates malformed changes', () => {
    expect(h(act('updated', { priority: 'high' }))).toBe('updated priority')
    expect(h({ ...act('updated'), changes: null as unknown as Record<string, unknown> })).toBe('updated the ticket')
  })
})

describe('humanizeActivity decision', () => {
  it('approved gates', () => {
    expect(h(act('decision', { requirements: [null, 'approved'] }))).toBe('approved the requirements')
    expect(h(act('decision', { plan: [null, 'approved'] }))).toBe('approved the plan')
  })
  it('requested changes with a note truncated to 200 characters', () => {
    expect(h(act('decision', { plan: [null, 'changes_requested'], note: 'Split it' }))).toBe('requested changes on the plan: Split it')
    const long = h({ ...act('decision', { plan: [null, 'changes_requested'] }), summary: 'x'.repeat(500) })
    expect(long.startsWith('requested changes on the plan: ')).toBe(true)
    expect(long.length).toBeLessThanOrEqual('requested changes on the plan: '.length + 203)
  })
  it('requested changes without a note, and unknown gate names', () => {
    expect(h(act('decision', { plan: [null, 'changes_requested'] }))).toBe('requested changes on the plan')
    expect(h(act('decision', { gate: [null, 'approved'] }))).toBe('approved')
    // The row the backend actually writes.
    expect(h(act('decision', { gate: [null, 'approved'], gate_name: [null, 'requirements'], note: [null, ''] }))).toBe('approved the requirements')
    expect(h(act('decision', { gate: [null, 'changes_requested'], gate_name: [null, 'plan'], note: [null, 'Split it'] }))).toBe(
      'requested changes on the plan: Split it',
    )
    expect(h(act('decision', {}))).toBe('recorded a decision')
  })
})
