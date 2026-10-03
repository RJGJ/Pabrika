import {
  createRouter, createWebHistory,
  type RouteLocationNormalized, type RouteLocationRaw, type Router, type RouterHistory,
} from 'vue-router'
import { useAuthStore, type AuthStatus } from '@/stores/auth'

declare module 'vue-router' {
  interface RouteMeta {
    /** Public auth screens (login, signup): signed-in users are sent home. */
    public?: boolean
  }
}

const BoardView = () => import('@/views/BoardView.vue')

export const routes = [
  { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { public: true } },
  { path: '/signup', name: 'signup', component: () => import('@/views/SignupView.vue'), meta: { public: true } },
  { path: '/', name: 'home', component: () => import('@/views/HomeRedirect.vue') },
  // `board` and `ticket` share the SAME component loader, so Vue patches (never remounts) BoardView when
  // the panel opens or closes: no board reload, no new stream. Do not key the RouterView by route.
  { path: '/p/:key', name: 'board', component: BoardView },
  { path: '/p/:key/t/:number', name: 'ticket', component: BoardView },
  { path: '/p/:key/settings', name: 'project-settings', component: () => import('@/views/ProjectSettingsView.vue') },
  { path: '/settings', name: 'account', component: () => import('@/views/AccountSettingsView.vue') },
  { path: '/:pathMatch(.*)*', name: 'not-found', component: () => import('@/views/NotFoundView.vue') },
]

/**
 * Pure guard decision (shared with the "Can't reach the server" retry):
 * returns a redirect location or `true` to continue.
 */
export function guardDecision(
  to: Pick<RouteLocationNormalized, 'name' | 'fullPath' | 'meta'>,
  auth: { status: AuthStatus; signupEnabled: boolean },
): RouteLocationRaw | true {
  // The server is unreachable: stay put, App.vue shows the full-page retry state.
  if (auth.status === 'unreachable') return true
  if (to.meta.public) {
    if (auth.status === 'authed') return '/'
    if (to.name === 'signup' && !auth.signupEnabled) return { name: 'login' }
    return true
  }
  if (auth.status !== 'authed') {
    return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : undefined }
  }
  return true
}

export function installGuards(router: Router): void {
  router.beforeEach(async (to) => {
    const auth = useAuthStore()
    if (auth.status === 'unknown') await auth.bootstrap()
    return guardDecision(to, auth)
  })
}

export function createAppRouter(history: RouterHistory = createWebHistory()): Router {
  const router = createRouter({ history, routes })
  installGuards(router)
  return router
}
