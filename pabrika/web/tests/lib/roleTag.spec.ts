import { describe, expect, it } from 'vitest'
import { parseRoleTag } from '@/lib/roleTag'

const bot = { bot: true }
const human = { bot: false }

describe('parseRoleTag', () => {
  it('parses all five roles', () => {
    for (const [tag, role] of [['orchestrator', 'Orchestrator'], ['researcher', 'Researcher'], ['developer', 'Developer'], ['reviewer', 'Reviewer'], ['qa', 'QA']] as const) {
      expect(parseRoleTag(`[${tag}] PLAN\nbody`, bot)).toMatchObject({ role: tag, roleLabel: role, type: 'PLAN' })
    }
  })

  it('parses each message type with its tone', () => {
    const cases: [string, string, string][] = [
      ['[orchestrator] STARTED', 'STARTED', 'neutral'],
      ['[orchestrator] ROUND 2/3', 'ROUND', 'neutral'],
      ['[orchestrator] BLOCKED', 'BLOCKED', 'red'],
      ['[orchestrator] DONE', 'DONE', 'neutral'],
      ['[researcher] REQUIREMENTS', 'REQUIREMENTS', 'neutral'],
      ['[developer] READY', 'READY', 'neutral'],
      ['[developer] FIXED', 'FIXED', 'neutral'],
      ['[reviewer] APPROVE', 'APPROVE', 'green'],
      ['[reviewer] CHANGES REQUESTED', 'CHANGES REQUESTED', 'orange'],
      ['[qa] PASS', 'PASS', 'green'],
      ['[qa] FAIL', 'FAIL', 'orange'],
    ]
    for (const [body, type, tone] of cases) expect(parseRoleTag(body, bot)).toMatchObject({ type, tone })
  })

  it('titles the message type', () => {
    expect(parseRoleTag('[reviewer] CHANGES REQUESTED', bot)?.typeLabel).toBe('Changes requested')
    expect(parseRoleTag('[qa] PASS', bot)?.typeLabel).toBe('Pass')
  })

  it('rejects human authors, a tag not at the start, lowercase types and a missing type', () => {
    expect(parseRoleTag('[developer] APPROVE', human)).toBeNull()
    expect(parseRoleTag(' [developer] PLAN', bot)).toBeNull()
    expect(parseRoleTag('Note: [developer] PLAN', bot)).toBeNull()
    expect(parseRoleTag('[developer] plan', bot)).toBeNull()
    expect(parseRoleTag('[developer] Plan the thing', bot)).toBeNull()
    expect(parseRoleTag('[developer]', bot)).toBeNull()
    expect(parseRoleTag('[developer] I think so', bot)).toBeNull()
    expect(parseRoleTag('[intern] PLAN', bot)).toBeNull()
    expect(parseRoleTag('', bot)).toBeNull()
    expect(parseRoleTag('[developer] PASSed', bot)).toBeNull()
  })

  it('strips the tag line only when it is exactly the first line', () => {
    expect(parseRoleTag('[developer] PLAN\nStep 1\nStep 2', bot)?.text).toBe('Step 1\nStep 2')
    expect(parseRoleTag('[developer] PLAN\r\nStep 1', bot)?.text).toBe('Step 1')
    expect(parseRoleTag('[developer] PLAN', bot)?.text).toBe('')
    expect(parseRoleTag('[developer] PLAN: add x\nmore', bot)?.text).toBe('[developer] PLAN: add x\nmore')
    expect(parseRoleTag('[orchestrator] ROUND 2/3\nx', bot)?.text).toBe('[orchestrator] ROUND 2/3\nx')
  })
})
