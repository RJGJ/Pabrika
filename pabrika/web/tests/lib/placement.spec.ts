import { describe, expect, it } from 'vitest'
import { computePlacement, isNoopDrop } from '@/lib/placement'

describe('computePlacement', () => {
  it.each([
    ['middle', ['a', 'b', 'c'], 1, { after: 'a' }],
    ['bottom', ['a', 'b', 'c'], 3, { after: 'c' }],
    ['top', ['a', 'b', 'c'], 0, { before: 'a' }],
    ['empty column', [], 0, { place: 'top' }],
    ['single, after', ['a'], 1, { after: 'a' }],
    ['single, before', ['a'], 0, { before: 'a' }],
  ])('%s', (_name, ids, idx, expected) => {
    expect(computePlacement(ids as string[], idx as number)).toEqual(expected)
  })

  it('clamps an out-of-range index', () => {
    expect(computePlacement(['a', 'b'], 99)).toEqual({ after: 'b' })
    expect(computePlacement(['a', 'b'], -3)).toEqual({ before: 'a' })
  })

  it('excludes the moved ticket from neighbours (caller strips it)', () => {
    const column = ['a', 'm', 'b']
    const without = column.filter((x) => x !== 'm')
    expect(computePlacement(without, 1)).toEqual({ after: 'a' })
  })
})

describe('isNoopDrop', () => {
  it('same column, same index is a no-op', () => {
    expect(isNoopDrop('todo', 'todo', 2, 2)).toBe(true)
  })
  it('same column, different index is a move', () => {
    expect(isNoopDrop('todo', 'todo', 2, 0)).toBe(false)
  })
  it('different column is never a no-op', () => {
    expect(isNoopDrop('todo', 'done', 0, 0)).toBe(false)
  })
})
