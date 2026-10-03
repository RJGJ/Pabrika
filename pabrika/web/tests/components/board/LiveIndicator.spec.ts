import { beforeEach, describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import LiveIndicator from '@/components/board/LiveIndicator.vue'
import { useBoardStore } from '@/stores/board'

beforeEach(() => setActivePinia(createPinia()))

describe('LiveIndicator', () => {
  it('is a polite live region that says Live or Reconnecting', async () => {
    const board = useBoardStore()
    const w = mount(LiveIndicator)
    expect(w.attributes('aria-live')).toBe('polite')
    expect(w.text()).toBe('')
    board.live = 'live'
    await w.vm.$nextTick()
    expect(w.text()).toBe('Live')
    board.live = 'reconnecting'
    await w.vm.$nextTick()
    expect(w.text()).toBe('Reconnecting')
    expect(w.html()).toContain('motion-safe:animate-pulse')
  })
})
