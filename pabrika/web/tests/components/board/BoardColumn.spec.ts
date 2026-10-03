import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import { VueDraggable } from 'vue-draggable-plus'

vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import BoardColumn from '@/components/board/BoardColumn.vue'
import { useAuthStore } from '@/stores/auth'
import { ME, ticket } from '../../stores/helpers'
import { seedBoard } from '../helpers'

async function mountColumn(status: 'backlog' | 'todo', opts: Parameters<typeof seedBoard>[1] = {}, extra = {}) {
  const board = seedBoard(
    [
      ticket(1, { status: 'backlog', position: 1024 }),
      ticket(2, { status: 'backlog', position: 2048 }),
      ticket(3, { status: 'backlog', position: 3072 }),
      ticket(4, { status: 'todo', position: 1024 }),
      ...(Object.values(extra) as never[]),
    ],
    opts,
  )
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      { path: '/p/:key/t/:number', name: 'ticket', component: { template: '<i/>' } },
    ],
  })
  await router.push('/p/WEB')
  const wrapper = mount(BoardColumn, {
    props: { status, title: status },
    global: { plugins: [router] },
  })
  const move = vi.spyOn(board, 'moveTicket').mockResolvedValue(true)
  const drag = wrapper.findComponent(VueDraggable)
  const props = drag.props() as Record<string, unknown>
  return { board, wrapper, move, props }
}

type Evt = Record<string, unknown>
const call = (fn: unknown, evt: Evt) => (fn as (e: Evt) => void)(evt)

beforeEach(() => {
  setActivePinia(createPinia())
  useAuthStore().user = ME
})

describe('BoardColumn', () => {
  it('renders the column cards and count', async () => {
    const { wrapper } = await mountColumn('backlog')
    expect(wrapper.findAll('[data-ticket-ref]').map((e) => e.attributes('data-ticket-ref'))).toEqual(['WEB-1', 'WEB-2', 'WEB-3'])
    expect(wrapper.text()).toContain('3')
  })

  it('same-column drop sends after (previous neighbour) without the moved id', async () => {
    const { move, props, board } = await mountColumn('backlog')
    board.startDrag('backlog')
    // WEB-1 dragged to the end (index 2 of the list that still includes it)
    call(props.onUpdate, { data: 't1', oldIndex: 0, newIndex: 2, oldDraggableIndex: 0, newDraggableIndex: 2 })
    expect(move).toHaveBeenCalledWith('t1', 'backlog', { after: 't3' })
  })

  it('drop at the top sends before the first remaining ticket', async () => {
    const { move, props } = await mountColumn('backlog')
    call(props.onUpdate, { data: 't3', oldIndex: 2, newIndex: 0, oldDraggableIndex: 2, newDraggableIndex: 0 })
    expect(move).toHaveBeenCalledWith('t3', 'backlog', { before: 't1' })
  })

  it('a drop at the original place sends no request', async () => {
    const { move, props } = await mountColumn('backlog')
    call(props.onUpdate, { data: 't2', oldIndex: 1, newIndex: 1, oldDraggableIndex: 1, newDraggableIndex: 1 })
    expect(move).not.toHaveBeenCalled()
  })

  it('cross-column drop (onAdd on the target) sends the target neighbours', async () => {
    const { move, props } = await mountColumn('todo')
    call(props.onAdd, { data: 't1', newIndex: 0, newDraggableIndex: 0 })
    expect(move).toHaveBeenCalledWith('t1', 'todo', { before: 't4' })
    call(props.onAdd, { data: 't2', newIndex: 1, newDraggableIndex: 1 })
    expect(move).toHaveBeenLastCalledWith('t2', 'todo', { after: 't4' })
  })

  it('a drop into an empty column sends place top', async () => {
    const { move, props, board } = await mountColumn('todo')
    board.columns = { ...board.columns, todo: [] }
    await flushPromises()
    call(props.onAdd, { data: 't1', newIndex: 0, newDraggableIndex: 0 })
    expect(move).toHaveBeenCalledWith('t1', 'todo', { place: 'top' })
  })

  it('dragging start and end update the store', async () => {
    const { board, props } = await mountColumn('backlog')
    call(props.onStart, {})
    expect(board.dragging).toEqual({ column: 'backlog' })
    call(props.onEnd, {})
    expect(board.dragging).toBeNull()
  })

  it('does not re-sync from the store while dragging, then syncs after the drop', async () => {
    const { board, wrapper } = await mountColumn('backlog')
    board.startDrag('backlog')
    board.columns = { ...board.columns, backlog: ['t3', 't2', 't1'] }
    await flushPromises()
    expect(wrapper.findAll('[data-ticket-ref]').map((e) => e.attributes('data-ticket-ref'))).toEqual(['WEB-1', 'WEB-2', 'WEB-3'])
    board.endDrag()
    await flushPromises()
    expect(wrapper.findAll('[data-ticket-ref]').map((e) => e.attributes('data-ticket-ref'))).toEqual(['WEB-3', 'WEB-2', 'WEB-1'])
  })

  it('dragging is disabled for viewers, archived projects and while filters are active', async () => {
    let c = await mountColumn('backlog')
    expect(c.props.disabled).toBe(false)
    c.board.setFilters({ q: 'x', priority: [], assignee: null, label: [] })
    await flushPromises()
    expect(c.wrapper.findComponent(VueDraggable).props('disabled')).toBe(true)

    setActivePinia(createPinia())
    useAuthStore().user = ME
    c = await mountColumn('backlog', { role: 'viewer' })
    expect(c.props.disabled).toBe(true)
    setActivePinia(createPinia())
    useAuthStore().user = ME
    c = await mountColumn('backlog', { archived: true })
    expect(c.props.disabled).toBe(true)
  })

  it('shows "n of m" while filtering', async () => {
    const { board, wrapper } = await mountColumn('backlog')
    board.setFilters({ q: 'Ticket 1', priority: [], assignee: null, label: [] })
    await flushPromises()
    expect(wrapper.text()).toContain('1 of 3')
  })

  it('viewer mode has no Add ticket button; editors do', async () => {
    const viewer = await mountColumn('backlog', { role: 'viewer' })
    expect(viewer.wrapper.text()).not.toContain('Add ticket')
    setActivePinia(createPinia())
    useAuthStore().user = ME
    const editor = await mountColumn('backlog', { role: 'editor' })
    expect(editor.wrapper.text()).toContain('Add ticket')
  })

  it('temp cards are not draggable or openable', async () => {
    const { board, wrapper } = await mountColumn('backlog')
    board.ticketsById = { ...board.ticketsById, tmp_1: ticket(0, { id: 'tmp_1', ref: '', title: 'saving' }) }
    board.columns = { ...board.columns, backlog: [...board.columns.backlog, 'tmp_1'] }
    await flushPromises()
    const temp = wrapper.find('[data-id="tmp_1"]')
    expect(temp.exists()).toBe(true)
    expect(temp.element.tagName).toBe('DIV')
    expect(temp.attributes('data-draggable')).toBeUndefined()
    expect(wrapper.find('[data-id="t1"]').attributes('data-draggable')).toBeDefined()
  })

  it('cards are focusable links to the ticket route preserving the query', async () => {
    const { wrapper } = await mountColumn('backlog')
    const a = wrapper.find('[data-id="t1"]')
    expect(a.element.tagName).toBe('A')
    expect(a.attributes('href')).toBe('/p/WEB/t/1')
  })
})
