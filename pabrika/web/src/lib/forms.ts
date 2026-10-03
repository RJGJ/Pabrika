import { ApiError, rateLimitMessage } from '@/api/client'

export interface FormErrors {
  fields: Record<string, string>
  form: string | null
}

/**
 * Map an API failure onto form fields. 422 `fields` map by name; `codeToField` routes specific
 * 409 codes (email_taken -> email, key_taken -> key, ...) to the nearest field.
 */
export function mapFormError(e: unknown, codeToField: Record<string, string> = {}): FormErrors {
  if (!(e instanceof ApiError)) return { fields: {}, form: 'Something went wrong' }
  if (e.status === 422 && e.fields && Object.keys(e.fields).length > 0) return { fields: { ...e.fields }, form: null }
  if (e.status === 429) return { fields: {}, form: rateLimitMessage(e) }
  const field = codeToField[e.code]
  if (field) return { fields: { [field]: e.message }, form: null }
  if (e.code === 'network') return { fields: {}, form: 'Network error, check your connection' }
  if (e.status >= 500 || e.status === 0) return { fields: {}, form: 'Something went wrong' }
  return { fields: {}, form: e.message }
}
