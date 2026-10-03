import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import type { Comment } from '@/api/types'
import CommentList from '@/components/ticket/CommentList.vue'
import userComment from '../../fixtures/comment-user.json'
import botComment from '../../fixtures/comment-bot.json'

const mine = { ...(userComment as Comment), id: 'c-mine', author: { ...(userComment as Comment).author, type: 'user' as const, id: 'u-me', bot: false } }
const theirs = { ...mine, id: 'c-theirs', author: { ...mine.author, id: 'u-other', name: 'Other' } }
const bot = { ...(botComment as Comment), id: 'c-bot' }

function render(over: { canEdit?: boolean; isOwner?: boolean }) {
  return mount(CommentList, {
    props: { comments: [mine, theirs, bot], canEdit: true, isOwner: false, meId: 'u-me', ...over },
    attachTo: document.body,
  })
}
const buttons = (w: ReturnType<typeof render>, id: string) =>
  w.find(`[data-comment-id="${id}"]`).findAll('button').map((b) => b.text())

describe('CommentList controls', () => {
  it('an editor sees edit and delete on their own comment only', () => {
    const w = render({})
    expect(buttons(w, 'c-mine')).toEqual(['Edit', 'Delete'])
    expect(buttons(w, 'c-theirs')).toEqual([])
    expect(buttons(w, 'c-bot')).toEqual([])
    w.unmount()
  })

  it('an agent comment shows the bot badge and no edit control for a human', () => {
    const w = render({})
    expect(w.find('[data-comment-id="c-bot"]').text().toLowerCase()).toContain('bot')
    expect(w.find('[data-comment-id="c-bot"]').text()).not.toContain('Edit')
    w.unmount()
  })

  it('an owner can delete every comment but only edit their own', () => {
    const w = render({ isOwner: true })
    expect(buttons(w, 'c-mine')).toEqual(['Edit', 'Delete'])
    expect(buttons(w, 'c-theirs')).toEqual(['Delete'])
    expect(buttons(w, 'c-bot')).toEqual(['Delete'])
    w.unmount()
  })

  it('viewers and archived projects (canEdit false) get no controls, even owners', () => {
    for (const isOwner of [false, true]) {
      const w = render({ canEdit: false, isOwner })
      for (const id of ['c-mine', 'c-theirs', 'c-bot']) expect(buttons(w, id)).toEqual([])
      w.unmount()
    }
  })

  it('marks edited comments', () => {
    const w = mount(CommentList, {
      props: { comments: [{ ...mine, edited_at: '2026-10-02T10:00:00Z' }], canEdit: false, isOwner: false, meId: 'u-me' },
    })
    expect(w.text()).toContain('edited')
  })
})
