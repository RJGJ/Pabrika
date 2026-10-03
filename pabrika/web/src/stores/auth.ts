import { defineStore } from 'pinia'
import { ref } from 'vue'
import { ApiError, resetUnauthorizedLatch } from '@/api/client'
import { auth as authApi } from '@/api/auth'
import type { AuthUser } from '@/api/types'
import { getRouter } from '@/router/instance'
import { notify } from '@/lib/toast'
import { useBoardStore } from './board'
import { useProjectsStore } from './projects'

export type AuthStatus = 'unknown' | 'authed' | 'anon' | 'unreachable'

export const useAuthStore = defineStore('auth', () => {
  const user = ref<AuthUser | null>(null)
  const status = ref<AuthStatus>('unknown')
  const signupEnabled = ref(true)
  let bootstrapping: Promise<void> | null = null

  async function loadConfig(): Promise<void> {
    try {
      signupEnabled.value = (await authApi.config()).signup_enabled
    } catch {
      // The server still answers 404 when signup is off, so assume it is enabled.
      signupEnabled.value = true
    }
  }

  /** Resolve who is signed in. 401 means signed out; network/5xx means the server is unreachable. */
  function bootstrap(): Promise<void> {
    if (bootstrapping) return bootstrapping
    bootstrapping = (async () => {
      await loadConfig()
      try {
        const me = await authApi.me()
        user.value = me.user
        status.value = 'authed'
        resetUnauthorizedLatch()
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          user.value = null
          status.value = 'anon'
        } else {
          user.value = null
          status.value = 'unreachable'
        }
      }
    })().finally(() => {
      bootstrapping = null
    })
    return bootstrapping
  }

  function setAuthed(u: AuthUser): void {
    user.value = u
    status.value = 'authed'
    resetUnauthorizedLatch()
  }

  async function login(email: string, password: string): Promise<void> {
    setAuthed((await authApi.login(email, password)).user)
  }

  async function signup(email: string, displayName: string, password: string): Promise<void> {
    try {
      setAuthed((await authApi.signup(email, displayName, password)).user)
    } catch (e) {
      // Signup was disabled while the page was open.
      if (e instanceof ApiError && e.status === 404) await loadConfig()
      throw e
    }
  }

  function clearStores(): void {
    useBoardStore().reset()
    useProjectsStore().reset()
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch {
      /* the session may already be gone; sign out locally either way */
    }
    user.value = null
    status.value = 'anon'
    clearStores()
    await getRouter()?.replace({ name: 'login' })
  }

  async function updateProfile(displayName: string): Promise<void> {
    user.value = (await authApi.updateProfile(displayName.trim())).user
  }

  /** The session stays valid; other sessions are signed out by the server. */
  async function changePassword(current: string, next: string): Promise<void> {
    await authApi.changePassword(current, next)
  }

  /** Called once (the client debounces) when any call returns 401 `unauthorized`. */
  function handleUnauthorized(): void {
    user.value = null
    status.value = 'anon'
    clearStores() // board.reset() also closes nothing yet; WP6 ties stream teardown to this reset
    const router = getRouter()
    if (!router) return
    const cur = router.currentRoute.value
    if (cur.name !== 'login' && cur.name !== 'signup') {
      const redirect = cur.fullPath !== '/' ? { redirect: cur.fullPath } : undefined
      void router.replace({ name: 'login', query: redirect })
    }
    notify('error', 'Session expired')
  }

  return {
    user, status, signupEnabled,
    bootstrap, login, signup, logout, updateProfile, changePassword, handleUnauthorized,
  }
})
