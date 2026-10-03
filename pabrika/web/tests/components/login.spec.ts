import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { ApiError } from '@/api/client'

vi.mock('@/api/auth', () => ({ auth: { config: vi.fn(), me: vi.fn(), login: vi.fn(), signup: vi.fn() } }))

import { auth as api } from '@/api/auth'
import LoginView from '@/views/LoginView.vue'
import SignupView from '@/views/SignupView.vue'
import { useAuthStore } from '@/stores/auth'

const mocked = api as unknown as Record<string, ReturnType<typeof vi.fn>>

function setup(component: object, path = '/login') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/login', name: 'login', component: LoginView },
      { path: '/signup', name: 'signup', component: SignupView },
      { path: '/', name: 'home', component: { template: '<div>home</div>' } },
      { path: '/p/:key', name: 'board', component: { template: '<div>board</div>' } },
    ],
  })
  void router.push(path)
  return router.isReady().then(() => ({
    router,
    wrapper: mount(component, { global: { plugins: [router] } }),
  }))
}

beforeEach(() => {
  setActivePinia(createPinia())
  for (const fn of Object.values(mocked)) fn.mockReset()
})

describe('LoginView', () => {
  it('shows the Create account link when signup is enabled', async () => {
    useAuthStore().signupEnabled = true
    const { wrapper } = await setup(LoginView)
    expect(wrapper.text()).toContain('Create account')
  })
  it('hides the Create account link when signup is disabled', async () => {
    useAuthStore().signupEnabled = false
    const { wrapper } = await setup(LoginView)
    expect(wrapper.text()).not.toContain('Create account')
  })
  it('shows the generic server message on invalid credentials', async () => {
    mocked.login.mockRejectedValue(new ApiError(401, 'invalid_credentials', 'Invalid email or password'))
    const { wrapper } = await setup(LoginView)
    await wrapper.find('#login-email').setValue('a@x.co')
    await wrapper.find('#login-password').setValue('nope')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Invalid email or password')
  })
  it('honors a safe redirect after login and ignores an unsafe one', async () => {
    mocked.login.mockResolvedValue({ user: { id: 'u', email: 'a@x.co', display_name: 'A', created_at: '' } })
    let { wrapper, router } = await setup(LoginView, '/login?redirect=/p/WEB')
    await wrapper.find('#login-email').setValue('a@x.co')
    await wrapper.find('#login-password').setValue('pw')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/p/WEB')
    ;({ wrapper, router } = await setup(LoginView, '/login?redirect=//evil.com'))
    await wrapper.find('#login-email').setValue('a@x.co')
    await wrapper.find('#login-password').setValue('pw')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/')
  })
  it('disables submit while pending', async () => {
    let resolve!: (v: unknown) => void
    mocked.login.mockReturnValue(new Promise((r) => (resolve = r)))
    const { wrapper } = await setup(LoginView)
    await wrapper.find('form').trigger('submit')
    expect(wrapper.find('button[type=submit]').attributes('disabled')).toBeDefined()
    resolve({ user: { id: 'u', email: 'a', display_name: 'A', created_at: '' } })
    await flushPromises()
  })
})

describe('SignupView', () => {
  it('validates client hints before calling the API', async () => {
    const { wrapper } = await setup(SignupView, '/signup')
    await wrapper.find('#signup-email').setValue('not-an-email')
    await wrapper.find('#signup-password').setValue('short')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(mocked.signup).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Enter a valid email')
    expect(wrapper.text()).toContain('at least 10 characters')
    expect(wrapper.text()).toContain('Display name must be 1 to 100')
  })
  it('shows email_taken under the email field', async () => {
    mocked.signup.mockRejectedValue(new ApiError(409, 'email_taken', 'An account with this email already exists'))
    const { wrapper } = await setup(SignupView, '/signup')
    await wrapper.find('#signup-email').setValue('a@x.co')
    await wrapper.find('#signup-name').setValue('Ada')
    await wrapper.find('#signup-password').setValue('long-enough-pw')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('#signup-email-error').text()).toContain('already exists')
  })
  it('redirects to /login when signup was disabled meanwhile (404)', async () => {
    mocked.signup.mockRejectedValue(new ApiError(404, 'not_found', 'not found'))
    mocked.config.mockResolvedValue({ signup_enabled: false })
    const { wrapper, router } = await setup(SignupView, '/signup')
    await wrapper.find('#signup-email').setValue('a@x.co')
    await wrapper.find('#signup-name').setValue('Ada')
    await wrapper.find('#signup-password').setValue('long-enough-pw')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('login')
    expect(useAuthStore().signupEnabled).toBe(false)
  })
})
