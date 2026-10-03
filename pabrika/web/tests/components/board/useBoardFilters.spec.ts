import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { defineComponent, h } from 'vue'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'

vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { useBoardFilters } from '@/composables/useBoardFilters'
import { useAuthStore } from '@/stores/auth'
import { ME, ticket } from '../../stores/helpers'
import { seedBoard } from '../helpers'

let api!: ReturnType<typeof useBoardFilters>
const Host = defineComponent({
  setup() {
    api = useBoardFilters()
    return () => h('div')
  },
})

async function setup(url: string) {
  const board = seedBoard([ticket(1, { title: 'Fix login' }), ticket(2, { title: 'Dark mode', priority: 'low' })])
  useAuthStore().user = ME
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key', name: 'board', component: Host },
      { path: '/p/:key/t/:number', name: 'ticket', component: Host },
    ],
  })
  await router.push(url)
  mount({ render: () => h(RouterView) }, { global: { plugins: [router] } })
  await flushPromises()
  return { board, router }
}

beforeEach(() => {
  vi.useFakeTimers()
  setActivePinia(createPinia())
})
afterEach(() => vi.useRealTimers())

describe('useBoardFilters', () => {
  it('restores filters from the URL query into the store', async () => {
    const { board } = await setup('/p/WEB?q=fix&priority=high,urgent&assignee=me&label=zzz')
    expect(board.filters).toEqual({ q: 'fix', priority: ['high', 'urgent'], assignee: 'me', label: [] })
    expect(board.filtersActive).toBe(true)
    expect(api.searchText.value).toBe('fix')
  })

  it('ignores unknown values', async () => {
    const { board } = await setup('/p/WEB?priority=bogus&assignee=ghost')
    expect(board.filters.priority).toEqual([])
    expect(board.filters.assignee).toBeNull()
    expect(board.filtersActive).toBe(false)
  })

  it('debounces search (200 ms) into the URL via replace, preserving other query params and the route', async () => {
    const { router, board } = await setup('/p/WEB/t/1?x=1')
    api.setSearch('dark')
    expect(router.currentRoute.value.query.q).toBeUndefined()
    await vi.advanceTimersByTimeAsync(199)
    expect(router.currentRoute.value.query.q).toBeUndefined()
    await vi.advanceTimersByTimeAsync(1)
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/p/WEB/t/1?x=1&q=dark')
    expect(router.currentRoute.value.name).toBe('ticket')
    expect(board.filteredColumns.backlog).toEqual(['t2'])
  })

  it('combines filters and clears them all', async () => {
    const { router, board } = await setup('/p/WEB')
    api.setPriority(['low'])
    await flushPromises()
    api.setAssignee('none')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ priority: 'low', assignee: 'none' })
    expect(board.filteredColumns.backlog).toEqual(['t2'])
    api.clear()
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({})
    expect(board.filtersActive).toBe(false)
  })
})
