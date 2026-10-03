import { describe, expect, it } from 'vitest'
import { pickHomeProject } from '@/lib/home'
import type { Project } from '@/api/types'

const p = (key: string) => ({ key }) as Project

describe('pickHomeProject', () => {
  it('prefers the remembered project (case-insensitive)', () => {
    expect(pickHomeProject([p('AAA'), p('WEB')], 'web')).toBe('WEB')
  })
  it('falls back to the first project when the remembered one is gone', () => {
    expect(pickHomeProject([p('AAA'), p('WEB')], 'GONE')).toBe('AAA')
    expect(pickHomeProject([p('AAA')], null)).toBe('AAA')
  })
  it('returns null for an empty list', () => {
    expect(pickHomeProject([], 'WEB')).toBeNull()
  })
})
