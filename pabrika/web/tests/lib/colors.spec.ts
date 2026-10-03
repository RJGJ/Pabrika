import { describe, expect, it } from 'vitest'
import { labelClasses, priorityClasses } from '@/lib/colors'
import { LABEL_COLORS, PRIORITIES } from '@/api/types'

describe('colors', () => {
  it.each([...LABEL_COLORS])('label %s has light and dark classes', (c) => {
    const cls = labelClasses(c)
    expect(cls).toContain(c)
    expect(cls).toContain('dark:')
  })
  it('unknown label colors fall back to gray', () => {
    expect(labelClasses('chartreuse')).toBe(labelClasses('gray'))
  })
  it.each([...PRIORITIES])('priority %s has classes', (p) => {
    expect(priorityClasses(p)).toContain('dark:')
  })
  it('priorities are visually distinct', () => {
    expect(new Set(PRIORITIES.map(priorityClasses)).size).toBe(PRIORITIES.length)
  })
  it('unknown priority falls back', () => {
    expect(priorityClasses('nope')).toBe(priorityClasses('low'))
  })
})
