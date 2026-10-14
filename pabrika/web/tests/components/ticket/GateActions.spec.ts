import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'

vi.mock('@/api/decisions', () => ({ decisions: { create: vi.fn(), list: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { ApiError } from '@/api/client'
import { decisions as decisionsApi } from '@/api/decisions'
import GateActions from '@/components/ticket/GateActions.vue'
import { notify } from '@/lib/toast'
import { useAuthStore } from '@/stores/auth'
import decision from '../../fixtures/decision.json'
import { ME, ticket, type Mocked } from '../../stores/helpers'
import { seedBoard } from '../helpers'

const D = decisionsApi as unknown as Mocked
const lab = (name: string) => ({ name })
let wrapper: VueWrapper | null = null

function mountActions(labels = [lab('awaiting-req-approval')], opts: Parameters<typeof seedBoard>[1] = {}) {
  const board = seedBoard([ticket(1)], opts)
  useAuthStore().user = ME
  wrapper = mount(GateActions, { props: { ticketId: 't1', ticketRef: 'WEB-1', labels }, attachTo: document.body })
  return board
}
const body = () => document.body
const btn = (text: string) => [...body().querySelectorAll('button')].find((b) => b.textContent?.trim() === text) as HTMLButtonElement | undefined
const confirmButton = (text: string) =>
  [...body().querySelectorAll('button')].filter((b) => b.textContent?.trim() === text).pop() as HTMLButtonElement
const click = async (text: string) => {
  btn(text)!.click()
  await flushPromises()
}

beforeEach(() => {
  setActivePinia(createPinia())
  for (const f of Object.values(D)) f.mockReset()
  ;(notify as unknown as ReturnType<typeof vi.fn>).mockReset()
})
afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  document.body.innerHTML = ''
})

describe('GateActions visibility', () => {
  it('shows both buttons to editors and owners on a gate label', () => {
    for (const role of ['editor', 'owner'] as const) {
      mountActions(undefined, { role })
      expect(btn('Approve')).toBeTruthy()
      expect(btn('Request changes')).toBeTruthy()
      wrapper!.unmount()
    }
  })
  it('hides them from viewers and on archived projects', () => {
    mountActions(undefined, { role: 'viewer' })
    expect(wrapper!.find('[data-testid=gate-actions]').exists()).toBe(false)
    wrapper!.unmount()
    mountActions(undefined, { role: 'owner', archived: true })
    expect(wrapper!.find('[data-testid=gate-actions]').exists()).toBe(false)
  })
  it('hides them without a gate label, including the merge gate', () => {
    for (const labels of [[], [lab('needs-dev')], [lab('awaiting-merge')], [lab('bug')]]) {
      mountActions(labels)
      expect(wrapper!.find('[data-testid=gate-actions]').exists()).toBe(false)
      wrapper!.unmount()
    }
  })
})

describe('GateActions flow', () => {
  it('approve asks for confirmation naming the gate, with an optional note', async () => {
    mountActions()
    D.create.mockResolvedValue({ ...decision, gate: 'requirements', verdict: 'approved' })
    await click('Approve')
    expect(body().textContent).toContain('Approve requirements for WEB-1?')
    expect(D.create).not.toHaveBeenCalled()
    const ta = body().querySelector('textarea') as HTMLTextAreaElement
    ta.value = '  looks good '
    ta.dispatchEvent(new Event('input'))
    await flushPromises()
    confirmButton('Approve').click()
    await flushPromises()
    expect(D.create).toHaveBeenCalledWith('t1', { gate: 'requirements', verdict: 'approved', note: 'looks good' })
    expect(wrapper!.emitted('recorded')).toHaveLength(1)
    expect(notify).toHaveBeenCalledWith('success', 'Approved')
  })

  it('approve works without a note (note omitted)', async () => {
    mountActions([lab('awaiting-plan-approval')])
    D.create.mockResolvedValue({ ...decision, verdict: 'approved' })
    await click('Approve')
    const confirm = [...body().querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Approve').pop()!
    confirm.click()
    await flushPromises()
    expect(D.create).toHaveBeenCalledWith('t1', { gate: 'plan', verdict: 'approved' })
  })

  it('request changes requires a note and blocks inline', async () => {
    mountActions()
    await click('Request changes')
    expect(body().textContent).toContain('Request changes on the requirements for WEB-1?')
    const confirm = [...body().querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Request changes').pop()!
    confirm.click()
    await flushPromises()
    expect(D.create).not.toHaveBeenCalled()
    expect(body().querySelector('[role=alert]')?.textContent).toContain('note is required')
    const ta = body().querySelector('textarea') as HTMLTextAreaElement
    ta.value = 'Add acceptance criteria'
    ta.dispatchEvent(new Event('input'))
    await flushPromises()
    D.create.mockResolvedValue({ ...decision, gate: 'requirements' })
    confirm.click()
    await flushPromises()
    expect(D.create).toHaveBeenCalledWith('t1', { gate: 'requirements', verdict: 'changes_requested', note: 'Add acceptance criteria' })
  })

  it('409 gate_not_open says the ticket moved on and asks the parent to refetch', async () => {
    mountActions()
    D.create.mockRejectedValue(new ApiError(409, 'gate_not_open', 'gate is not open'))
    await click('Approve')
    const confirm = [...body().querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Approve').pop()!
    confirm.click()
    await flushPromises()
    expect(notify).toHaveBeenCalledWith('info', 'This ticket moved on')
    expect(wrapper!.emitted('stale')).toHaveLength(1)
    expect(wrapper!.emitted('recorded')).toBeUndefined()
  })

  it('a 422 shows the server message inline and keeps the dialog open', async () => {
    mountActions()
    D.create.mockRejectedValue(new ApiError(422, 'validation_failed', 'bad', { note: 'Note is too long' }))
    await click('Approve')
    const confirm = [...body().querySelectorAll('button')].filter((b) => b.textContent?.trim() === 'Approve').pop()!
    confirm.click()
    await flushPromises()
    expect(body().querySelector('[role=alert]')?.textContent).toContain('Note is too long')
    expect(body().querySelector('textarea')).toBeTruthy()
  })
})
