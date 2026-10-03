import { describe, expect, it } from 'vitest'
import { safeRedirect } from '@/lib/redirect'

describe('safeRedirect', () => {
  it.each([
    ['/p/WEB', '/p/WEB'],
    ['/p/WEB/t/12?q=a&priority=high', '/p/WEB/t/12?q=a&priority=high'],
    ['/settings', '/settings'],
    ['/', '/'],
  ])('keeps %s', (input, out) => expect(safeRedirect(input)).toBe(out))

  it.each([
    '//evil.com',
    '///evil.com',
    '/\\evil.com',
    '\\\\evil.com',
    'https://evil.com',
    'http://evil.com/x',
    'javascript:alert(1)',
    'evil.com',
    '',
    '/login',
    '/login?redirect=/p/WEB',
    '/signup',
    '/signup#x',
    '/\t/evil.com',
    '/\n/evil.com',
    '/foo\\bar',
  ])('rejects %j', (input) => expect(safeRedirect(input)).toBe('/'))

  it('rejects non-strings and arrays', () => {
    expect(safeRedirect(undefined)).toBe('/')
    expect(safeRedirect(null)).toBe('/')
    expect(safeRedirect(['/p/WEB'])).toBe('/')
  })
  it('does not reject paths that merely start with login', () => {
    expect(safeRedirect('/loginx')).toBe('/loginx')
  })
})
