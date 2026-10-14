import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { h } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'

vi.mock('@/api/projects', () => ({ projects: { list: vi.fn(), get: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import AppSidebar from '@/components/layout/AppSidebar.vue'
import { SidebarProvider } from '@/components/ui/sidebar'
import { useAuthStore } from '@/stores/auth'
import { useProjectsStore } from '@/stores/projects'
import { ME, ticket } from '../../stores/helpers'
import { seedBoard } from '../helpers'

const lab = (name: string) => ({ id: `l-${name}`, project_id: 'p1', name, color: 'gray' })
let wrapper: VueWrapper | null = null

async function render(path: string, tickets = [ticket(1)]) {
  seedBoard(tickets)
  useAuthStore().user = ME
  const projects = useProjectsStore()
  projects.list = [{ id: 'p1', key: 'WEB', name: 'Web', description: '', archived_at: null, created_at: '', updated_at: '', role: 'owner' }]
  projects.loaded = true
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      { path: '/p/:key/activity', name: 'project-activity', component: { template: '<i/>' } },
      { path: '/settings', name: 'account', component: { template: '<i/>' } },
      { path: '/', name: 'home', component: { template: '<i/>' } },
    ],
  })
  await router.push(path)
  window.matchMedia ??= ((q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} })) as never
  wrapper = mount({ render: () => h(SidebarProvider, null, { default: () => h(AppSidebar) }) }, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('AppSidebar workflow cues', () => {
  it('shows the gate count and an Activity link on the open project', async () => {
    await render('/p/WEB', [
      ticket(1, { labels: [lab('awaiting-req-approval')] }),
      ticket(2, { labels: [lab('awaiting-merge')] }),
      ticket(3, { labels: [lab('needs-dev')] }),
    ])
    expect(document.body.querySelector('[data-testid=gate-count]')?.textContent).toContain('2')
    expect(document.body.querySelector('[data-testid=sidebar-activity]')?.getAttribute('href')).toBe('/p/WEB/activity')
  })

  it('shows no count when nothing waits', async () => {
    await render('/p/WEB', [ticket(1, { labels: [lab('needs-dev')] })])
    expect(document.body.querySelector('[data-testid=gate-count]')).toBeNull()
  })

  it('shows neither on another project page', async () => {
    await render('/settings')
    expect(document.body.querySelector('[data-testid=sidebar-activity]')).toBeNull()
  })
})
