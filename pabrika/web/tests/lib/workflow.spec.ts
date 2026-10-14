import { describe, expect, it } from 'vitest'
import { classifyLabels, gateOf, needsAttention, prLink, roundTone, showRound, stageText } from '@/lib/workflow'

const L = (...names: string[]) => names.map((name) => ({ id: name, name, color: 'gray' }))

describe('classifyLabels', () => {
  it('classifies every workflow label', () => {
    for (const s of ['needs-requirements', 'awaiting-req-approval', 'awaiting-plan-approval', 'needs-dev', 'needs-review', 'needs-qa', 'awaiting-merge']) {
      const c = classifyLabels(L(s))
      expect(c.stage).toBe(s)
      expect(c.other).toEqual([])
      expect(c.conflict).toBe(false)
    }
    expect(classifyLabels(L('epic')).kind).toBe('epic')
    expect(classifyLabels(L('task')).kind).toBe('task')
    expect(classifyLabels(L('blocked', 'changes-requested')).flags).toEqual(['blocked', 'changes-requested'])
  })

  it('keeps unknown labels in other, as the original objects', () => {
    const labels = L('bug', 'needs-review', 'ui')
    const c = classifyLabels(labels)
    expect(c.other).toEqual([labels[0], labels[2]])
    expect(c.hasWorkflow).toBe(true)
  })

  it('has no workflow for plain projects', () => {
    const c = classifyLabels(L('bug'))
    expect(c).toMatchObject({ kind: null, stage: null, flags: [], conflict: false, hasWorkflow: false })
  })

  it('picks one stage deterministically and flags a conflict', () => {
    const a = classifyLabels(L('needs-qa', 'needs-dev'))
    const b = classifyLabels(L('needs-dev', 'needs-qa'))
    expect(a.stage).toBe('needs-dev')
    expect(b.stage).toBe('needs-dev')
    expect(a.conflict).toBe(true)
    expect(a.other).toEqual([])
  })

  it('is case sensitive', () => {
    expect(classifyLabels(L('Needs-Dev')).stage).toBeNull()
  })
})

describe('stage text, gates and attention', () => {
  it('names stages', () => {
    expect(stageText('needs-dev')).toBe('Needs dev')
    expect(stageText('needs-review')).toBe('Needs review')
    expect(stageText('needs-qa')).toBe('Needs QA')
    expect(stageText('awaiting-req-approval')).toBe('Needs you: approve requirements')
    expect(stageText('awaiting-plan-approval')).toBe('Needs you: approve plan')
    expect(stageText('awaiting-merge')).toBe('Needs you: merge')
  })
  it('maps gate labels to gates; merge has none', () => {
    expect(gateOf('awaiting-req-approval')).toBe('requirements')
    expect(gateOf('awaiting-plan-approval')).toBe('plan')
    expect(gateOf('awaiting-merge')).toBeNull()
    expect(gateOf('needs-dev')).toBeNull()
    expect(gateOf(null)).toBeNull()
  })
  it('needs attention for any awaiting stage or blocked', () => {
    expect(needsAttention(L('awaiting-merge'))).toBe(true)
    expect(needsAttention(L('awaiting-req-approval'))).toBe(true)
    expect(needsAttention(L('blocked', 'needs-dev'))).toBe(true)
    expect(needsAttention(L('needs-dev'))).toBe(false)
    expect(needsAttention(L('changes-requested'))).toBe(false)
    expect(needsAttention([])).toBe(false)
  })
})

describe('prLink', () => {
  it('accepts https and shortens pull URLs', () => {
    expect(prLink('https://github.com/a/b/pull/123')).toEqual({ href: 'https://github.com/a/b/pull/123', text: '#123' })
    expect(prLink('https://github.com/a/b/pull/123/')?.text).toBe('#123')
    expect(prLink('https://example.com/review/9')?.text).toBe('PR')
  })
  it('rejects bad urls', () => {
    for (const u of ['javascript:alert(1)', 'http://x.io/pull/1', 'not a url', '', null, undefined, 'ftp://x.io/a']) {
      expect(prLink(u as string)).toBeNull()
    }
  })
})

describe('round', () => {
  it('tones', () => {
    expect(roundTone({ n: 1, max: 3 })).toBe('normal')
    expect(roundTone({ n: 2, max: 3 })).toBe('amber')
    expect(roundTone({ n: 3, max: 3 })).toBe('red')
    expect(roundTone({ n: 4, max: 3 })).toBe('red')
  })
  it('shows only with a round and changes-requested or needs-dev', () => {
    const r = { n: 1, max: 3 }
    expect(showRound(classifyLabels(L('needs-dev')), r)).toBe(true)
    expect(showRound(classifyLabels(L('changes-requested')), r)).toBe(true)
    expect(showRound(classifyLabels(L('needs-review')), r)).toBe(false)
    expect(showRound(classifyLabels(L('needs-dev')), null)).toBe(false)
    expect(showRound(classifyLabels(L('needs-dev')), undefined)).toBe(false)
  })
})
