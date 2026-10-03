import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/client'
import { mapFormError } from '@/lib/forms'

describe('mapFormError', () => {
  it('maps 422 fields', () => {
    const e = new ApiError(422, 'validation_failed', 'Invalid', { email: 'bad' })
    expect(mapFormError(e)).toEqual({ fields: { email: 'bad' }, form: null })
  })
  it('routes 409 codes to a field', () => {
    const e = new ApiError(409, 'email_taken', 'Email taken')
    expect(mapFormError(e, { email_taken: 'email' })).toEqual({ fields: { email: 'Email taken' }, form: null })
  })
  it('shows the rate limit message on 429', () => {
    const e = new ApiError(429, 'rate_limited', 'x', undefined, 30)
    expect(mapFormError(e).form).toBe('Too many attempts, try again in 30 seconds')
  })
  it('keeps the server message for other 4xx (login failure)', () => {
    const e = new ApiError(401, 'invalid_credentials', 'Invalid email or password')
    expect(mapFormError(e).form).toBe('Invalid email or password')
  })
  it('generic text for network, 5xx and unknown errors', () => {
    expect(mapFormError(new ApiError(0, 'network', 'x')).form).toBe('Network error, check your connection')
    expect(mapFormError(new ApiError(500, 'internal', 'x')).form).toBe('Something went wrong')
    expect(mapFormError(new Error('boom')).form).toBe('Something went wrong')
  })
})
