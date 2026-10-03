import { describe, expect, it } from 'vitest'
import { isValidKey, normalizeKey, suggestKey } from '@/lib/projectKey'

describe('projectKey', () => {
  it.each([
    ['Pabrika', 'PAB'],
    ['My Website', 'MW'],
    ['Customer Support Portal', 'CSP'],
    ['a', 'A'],
    ['', ''],
    ['123 !!', ''],
    ['Alpha Beta Gamma Delta Epsilon Zeta Eta', 'ABGDEZ'],
    ['Go', 'GO'],
  ])('suggests %j -> %j', (name, key) => expect(suggestKey(name)).toBe(key))

  it('normalizes typed input', () => {
    expect(normalizeKey('we-b1 x')).toBe('WEBX')
    expect(normalizeKey('abcdefghij')).toBe('ABCDEF')
  })
  it('validates 2 to 6 letters', () => {
    expect(isValidKey('WE')).toBe(true)
    expect(isValidKey('W')).toBe(false)
    expect(isValidKey('WEBSITE7')).toBe(false)
    expect(isValidKey('web')).toBe(false)
  })
})
