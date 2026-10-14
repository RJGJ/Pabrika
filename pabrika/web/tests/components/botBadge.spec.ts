import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import BotBadge from '@/components/common/BotBadge.vue'

describe('BotBadge', () => {
  it('renders exactly the bot badge without a role', () => {
    const w = mount(BotBadge)
    expect(w.text()).toBe('bot')
    expect(w.find('[data-testid=role-chip]').exists()).toBe(false)
    expect(w.element.className).toContain('uppercase')
  })
  it('adds a role chip next to it', () => {
    const w = mount(BotBadge, { props: { role: 'Developer' } })
    expect(w.text()).toContain('bot')
    expect(w.get('[data-testid=role-chip]').text()).toBe('Developer')
  })
})
