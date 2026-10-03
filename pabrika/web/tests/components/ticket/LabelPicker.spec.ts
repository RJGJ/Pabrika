import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { Label } from '@/api/types'

vi.mock('@/api/labels', () => ({ labels: { create: vi.fn(), list: vi.fn() } }))
vi.mock('@/lib/toast', () => ({ notify: vi.fn() }))

import { labels as labelsApi } from '@/api/labels'
import LabelPicker from '@/components/ticket/LabelPicker.vue'
import { useBoardStore } from '@/stores/board'

const lbl = (id: string, name: string): Label => ({ id, project_id: 'P1', name, color: 'blue' })
const create = labelsApi.create as unknown as ReturnType<typeof vi.fn>

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn() // jsdom lacks it; reka's listbox calls it on highlight
  setActivePinia(createPinia())
  create.mockReset()
  const board = useBoardStore()
  board.project = { id: 'P1', key: 'WEB' } as never
})
afterEach(() => {
  document.body.innerHTML = ''
})

async function openAndType(text: string) {
  const w = mount(LabelPicker, {
    props: { modelValue: [], labels: [lbl('L1', 'design'), lbl('L2', 'bug')], canCreate: true },
    attachTo: document.body,
  })
  await w.find('button[role=combobox]').trigger('click')
  await flushPromises()
  const input = document.body.querySelector('input[data-slot=command-input]') as HTMLInputElement
  // Type character by character like a user: each keystroke must keep the create option visible.
  let acc = ''
  for (const ch of text) {
    acc += ch
    input.value = acc
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await flushPromises()
  }
  return w
}

const itemTexts = () => [...document.body.querySelectorAll('[data-slot=command-item]')].map((e) => e.textContent?.trim())

describe('LabelPicker', () => {
  it('shows "Create label" for a multi-character new name and creates it', async () => {
    create.mockResolvedValue(lbl('L3', 'urgent-fix'))
    const w = await openAndType('urgent-fix')
    expect(itemTexts()).toEqual(['Create label "urgent-fix"'])
    ;(document.body.querySelector('[data-slot=command-item]') as HTMLElement).click()
    await flushPromises()
    expect(create).toHaveBeenCalledWith('WEB', 'urgent-fix', 'gray')
    expect(w.emitted('update:modelValue')?.[0]).toEqual([['L3']])
    w.unmount()
  })

  it('filters existing labels by name', async () => {
    const w = await openAndType('des')
    expect(itemTexts()).toContain('design')
    expect(itemTexts()).not.toContain('bug')
    w.unmount()
  })
})
