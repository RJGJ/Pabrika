import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { ApiError } from '@/api/client'

vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import InlineAddTicket from '@/components/board/InlineAddTicket.vue'
import { ticket } from '../../stores/helpers'
import { seedBoard } from '../helpers'

beforeEach(() => setActivePinia(createPinia()))

async function open() {
  const board = seedBoard([ticket(1)])
  const create = vi.spyOn(board, 'createTicket')
  const wrapper = mount(InlineAddTicket, { props: { status: 'todo' }, attachTo: document.body })
  await wrapper.find('button').trigger('click')
  await flushPromises()
  return { board, create, wrapper }
}

describe('InlineAddTicket', () => {
  it('Enter creates the ticket, clears the field and keeps it open for rapid entry', async () => {
    const { create, wrapper } = await open()
    create.mockResolvedValue({} as never)
    const input = wrapper.find('input')
    await input.setValue('  First  ')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(create).toHaveBeenCalledWith('todo', 'First')
    expect((input.element as HTMLInputElement).value).toBe('')
    expect(wrapper.find('input').exists()).toBe(true)
    wrapper.unmount()
  })

  it('ignores an empty title', async () => {
    const { create, wrapper } = await open()
    await wrapper.find('input').trigger('keydown', { key: 'Enter' })
    expect(create).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('restores the text and shows the field message on a 422', async () => {
    const { create, wrapper } = await open()
    create.mockRejectedValue(new ApiError(422, 'validation_failed', 'bad', { title: 'Title is too long' }))
    const input = wrapper.find('input')
    await input.setValue('Oops')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect((input.element as HTMLInputElement).value).toBe('Oops')
    expect(wrapper.text()).toContain('Title is too long')
    wrapper.unmount()
  })

  it('restores the text on other errors too', async () => {
    const { create, wrapper } = await open()
    create.mockRejectedValue(new ApiError(500, 'internal', 'boom'))
    const input = wrapper.find('input')
    await input.setValue('Keep me')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect((input.element as HTMLInputElement).value).toBe('Keep me')
    wrapper.unmount()
  })

  it('Escape and blur-when-empty close the field', async () => {
    const { wrapper } = await open()
    await wrapper.find('input').trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('input').exists()).toBe(false)
    await wrapper.find('button').trigger('click')
    await flushPromises()
    await wrapper.find('input').trigger('blur')
    expect(wrapper.find('input').exists()).toBe(false)
    wrapper.unmount()
  })
})
