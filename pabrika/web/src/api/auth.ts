import { api, request } from './client'
import type { AuthConfig, AuthMe, AuthUser } from './types'

export const auth = {
  config: () => api.get<AuthConfig>('/auth/config', { silent: true }),
  /** The bootstrap call: a 401 means signed out and must not trigger the global redirect. */
  me: () => api.get<AuthMe>('/auth/me', { skipUnauthorized: true }),
  login: (email: string, password: string) => api.post<{ user: AuthUser }>('/auth/login', { email, password }),
  signup: (email: string, display_name: string, password: string) =>
    api.post<{ user: AuthUser }>('/auth/signup', { email, display_name, password }),
  logout: () => request('POST', '/auth/logout', { silent: true }),
  updateProfile: (display_name: string) => api.patch<{ user: AuthUser }>('/auth/me', { display_name }),
  changePassword: (current_password: string, new_password: string) =>
    api.post('/auth/me/password', { current_password, new_password }),
}
