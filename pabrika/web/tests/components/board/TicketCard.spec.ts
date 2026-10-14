import { beforeEach, describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import type { TicketSummary } from '@/api/types'
import TicketCard from '@/components/board/TicketCard.vue'
import { useAuthStore } from '@/stores/auth'
import { ME, ticket } from '../../stores/helpers'
import { seedBoard } from '../helpers'

const lab = (name: string) => ({ id: `l-${name}`, project_id: 'p1', name, color: 'gray' })

async function render(over: Partial<TicketSummary>) {
  const t = ticket(1, over)
  seedBoard([t])
  useAuthStore().user = ME
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/p/:key', name: 'board', component: { template: '<i/>' } },
      { path: '/p/:key/t/:number', name: 'ticket', component: { template: '<i/>' } },
    ],
  })
  await router.push('/p/WEB')
  const w = mount(TicketCard, { props: { id: t.id }, global: { plugins: [router] }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

beforeEach(() => setActivePinia(createPinia()))

const wf = (over: Record<string, unknown>) => ({ pr_url: null, branch: null, round: null, ...over }) as TicketSummary['workflow']

describe('TicketCard workflow', () => {
  it('shows the stage as text and the kind tag; other labels stay chips', async () => {
    const { w } = await render({ labels: [lab('task'), lab('needs-review'), lab('bug')] })
    expect(w.get('[data-testid=stage-badge]').text()).toBe('Needs review')
    expect(w.get('[data-testid=kind-tag]').text()).toBe('task')
    expect(w.text()).toContain('bug')
    expect(w.text()).not.toContain('needs-review')
    w.unmount()
  })

  it('gate stages read "Needs you"', async () => {
    const { w } = await render({ labels: [lab('awaiting-plan-approval')] })
    expect(w.get('[data-testid=stage-badge]').text()).toBe('Needs you: approve plan')
    w.unmount()
  })

  it('shows blocked and changes-requested flags', async () => {
    const { w } = await render({ labels: [lab('blocked'), lab('changes-requested')] })
    expect(w.get('[data-testid=flag-blocked]').text()).toBe('Blocked')
    expect(w.get('[data-testid=flag-changes-requested]').text()).toBe('Changes requested')
    w.unmount()
  })

  it('renders exactly as before without workflow labels', async () => {
    const { w } = await render({ labels: [lab('bug')] })
    expect(w.find('[data-testid=stage-badge]').exists()).toBe(false)
    expect(w.find('[data-testid=kind-tag]').exists()).toBe(false)
    expect(w.find('[data-testid=pr-chip]').exists()).toBe(false)
    expect(w.text()).toContain('bug')
    w.unmount()
  })

  it('renders a PR chip with a safe link', async () => {
    const { w } = await render({ workflow: wf({ pr_url: 'https://github.com/a/b/pull/42' }) })
    const a = w.get('[data-testid=pr-chip]')
    expect(a.attributes('href')).toBe('https://github.com/a/b/pull/42')
    expect(a.attributes('target')).toBe('_blank')
    expect(a.attributes('rel')).toBe('noopener noreferrer')
    expect(a.text()).toContain('#42')
    w.unmount()
  })

  it('renders no chip for a bad URL', async () => {
    for (const pr_url of ['javascript:alert(1)', 'http://github.com/a/b/pull/1', 'garbage']) {
      const { w } = await render({ workflow: wf({ pr_url }) })
      expect(w.find('[data-testid=pr-chip]').exists()).toBe(false)
      w.unmount()
    }
  })

  it('clicking the PR chip does not open the panel', async () => {
    const { w, router } = await render({ workflow: wf({ pr_url: 'https://github.com/a/b/pull/42' }) })
    await w.get('[data-testid=pr-chip]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('board')
    await w.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('ticket')
    w.unmount()
  })

  it('round badge: hidden, normal, amber at N-1, red at the cap', async () => {
    const cases: [string[], { n: number; max: number } | null, string | null][] = [
      [['needs-review'], { n: 1, max: 3 }, null],
      [['needs-dev'], null, null],
      [['needs-dev'], { n: 1, max: 3 }, 'normal'],
      [['needs-dev'], { n: 2, max: 3 }, 'amber'],
      [['changes-requested', 'needs-dev'], { n: 3, max: 3 }, 'red'],
    ]
    for (const [names, round, tone] of cases) {
      const { w } = await render({ labels: names.map(lab), workflow: wf({ round }) })
      const badge = w.find('[data-testid=round-badge]')
      if (tone === null) expect(badge.exists()).toBe(false)
      else {
        expect(badge.attributes('data-tone')).toBe(tone)
        expect(badge.text()).toBe(`Round ${round!.n}/${round!.max}`)
      }
      w.unmount()
    }
  })
})
