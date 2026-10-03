import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'

vi.mock('@/api/tickets', () => ({
  tickets: { listAll: vi.fn(), get: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), move: vi.fn() },
}))
vi.mock('@/api/comments', () => ({
  comments: { listAll: vi.fn(), add: vi.fn(), update: vi.fn(), remove: vi.fn(), activity: vi.fn() },
}))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { ApiError } from '@/api/client'
import { comments as commentsApi } from '@/api/comments'
import { tickets as ticketsApi } from '@/api/tickets'
import StatusSelect from '@/components/ticket/StatusSelect.vue'
import TicketPanel from '@/components/ticket/TicketPanel.vue'
import { useAuthStore } from '@/stores/auth'
import botComment from '../../fixtures/comment-bot.json'
import { full, ME, ticket, type Mocked } from '../../stores/helpers'
import { seedBoard } from '../helpers'

const T = ticketsApi as unknown as Mocked
const C = commentsApi as unknown as Mocked

let wrapper: VueWrapper | null = null

async function open(path: string, opts: Parameters<typeof seedBoard>[1] = {}) {
  const board = seedBoard([ticket(1, { status: 'todo' }), ticket(2)], opts)
  useAuthStore().user = ME
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      { path: '/p/:key/t/:number', name: 'ticket', component: { template: '<i/>' } },
    ],
  })
  await router.push(path)
  wrapper = mount(TicketPanel, { global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { board, router }
}

const body = () => document.body

beforeEach(() => {
  setActivePinia(createPinia())
  for (const m of [T, C]) for (const f of Object.values(m)) f.mockReset()
  T.get.mockImplementation(async (id: string) => full(ticket(id === 't1' || id === 'WEB-1' ? 1 : 2, { status: id === 't1' || id === 'WEB-1' ? 'todo' : 'backlog' }), 'Hello **world**'))
  C.listAll.mockResolvedValue([botComment])
  C.activity.mockResolvedValue({ items: [], next_cursor: null })
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('TicketPanel', () => {
  it('loads the full ticket, comments and the first activity page in parallel by id', async () => {
    await open('/p/WEB/t/1')
    expect(T.get).toHaveBeenCalledWith('t1', expect.anything())
    expect(C.listAll).toHaveBeenCalledWith('t1', expect.anything())
    expect(C.activity).toHaveBeenCalledWith('t1', null, expect.anything())
    expect(body().textContent).toContain('WEB-1')
    expect(body().innerHTML).toContain('<strong>world</strong>')
    expect(body().textContent).toContain('bot')
  })

  it('editors get a title input, a Status select, comment form and delete', async () => {
    await open('/p/WEB/t/1', { role: 'editor' })
    expect(body().querySelector('#ticket-title')).toBeTruthy()
    expect(wrapper!.findComponent(StatusSelect).exists()).toBe(true)
    expect(body().textContent).toContain('Delete ticket')
    expect(body().querySelector('textarea[aria-label="New comment"]')).toBeTruthy()
  })

  it('viewer mode renders plain text: no inputs, no status select, no comment form, no delete', async () => {
    await open('/p/WEB/t/1', { role: 'viewer' })
    expect(body().querySelector('#ticket-title')).toBeNull()
    expect(body().querySelector('[role=combobox]')).toBeNull()
    expect(body().querySelector('textarea')).toBeNull()
    expect(body().textContent).not.toContain('Delete ticket')
    expect(body().querySelector('h2.break-words')?.textContent).toBe('Ticket 1')
    expect(body().textContent).toContain('Hello')
  })

  it('archived projects are read-only for owners too', async () => {
    await open('/p/WEB/t/1', { role: 'owner', archived: true })
    expect(body().querySelector('#ticket-title')).toBeNull()
    expect(body().textContent).not.toContain('Delete ticket')
  })

  it('the Status select moves the ticket to the bottom of the chosen column', async () => {
    const { board } = await open('/p/WEB/t/1')
    const move = vi.spyOn(board, 'moveTicket').mockResolvedValue(true)
    wrapper!.findComponent(StatusSelect).vm.$emit('update:modelValue', 'done')
    expect(move).toHaveBeenCalledWith('t1', 'done', { place: 'bottom' })
    move.mockClear()
    wrapper!.findComponent(StatusSelect).vm.$emit('update:modelValue', 'todo') // unchanged
    expect(move).not.toHaveBeenCalled()
  })

  it('a cold deep link to a ticket not on the board fetches it by reference', async () => {
    const { board } = await open('/p/WEB/t/7')
    expect(T.get).toHaveBeenCalledWith('WEB-7', expect.anything())
    void board
  })

  it('shows "Ticket not found" on 404 and for a non-integer number', async () => {
    T.get.mockRejectedValue(new ApiError(404, 'not_found', 'nope'))
    await open('/p/WEB/t/999')
    expect(body().textContent).toContain('Ticket not found')
    wrapper!.unmount()
    document.body.innerHTML = ''
    setActivePinia(createPinia())
    await open('/p/WEB/t/abc')
    expect(body().textContent).toContain('Ticket not found')
    expect(T.get).toHaveBeenCalledTimes(1) // only the first one; abc never fetched
  })

  it('keeps an unsaved title draft and shows "Changed by someone else" when the server copy changes', async () => {
    const { board } = await open('/p/WEB/t/1')
    const input = body().querySelector<HTMLInputElement>('#ticket-title')!
    input.value = 'My draft'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    board.fullById = { ...board.fullById, t1: full(ticket(1, { status: 'todo', title: 'Server rename' }), 'Hello **world**') }
    await flushPromises()
    expect(body().querySelector<HTMLInputElement>('#ticket-title')!.value).toBe('My draft')
    expect(body().textContent).toContain('Changed by someone else')
  })

  it('Escape in the title input reverts an edited title first, then closes the panel', async () => {
    const { router } = await open('/p/WEB/t/1')
    const input = body().querySelector<HTMLInputElement>('#ticket-title')!
    const original = input.value
    input.value = 'half typed'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    const esc = () => input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    esc()
    await flushPromises()
    expect(input.value).toBe(original)
    expect(router.currentRoute.value.name).toBe('ticket') // first Escape only reverted
    esc()
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('board') // nothing to revert: closes
  })

  it('follows the server copy when there is no unsaved draft', async () => {
    const { board } = await open('/p/WEB/t/1')
    board.fullById = { ...board.fullById, t1: full(ticket(1, { status: 'todo', title: 'Server rename' }), 'Hello **world**') }
    await flushPromises()
    expect(body().querySelector<HTMLInputElement>('#ticket-title')!.value).toBe('Server rename')
    expect(body().textContent).not.toContain('Changed by someone else')
  })

  it('refetches comments and activity when a comment event names the ticket', async () => {
    const { board } = await open('/p/WEB/t/1')
    C.listAll.mockClear()
    C.activity.mockClear()
    board.commentVersions = { t1: 1 }
    await flushPromises()
    expect(C.listAll).toHaveBeenCalledTimes(1)
    expect(C.activity).toHaveBeenCalledTimes(1)
  })

  it('closing keeps the query string', async () => {
    const { router } = await open('/p/WEB/t/1?priority=high')
    wrapper!.findComponent({ name: 'Sheet' }).vm.$emit('update:open', false)
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/p/WEB?priority=high')
  })
})
