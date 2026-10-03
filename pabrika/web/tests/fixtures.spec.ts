// Typed drift check: each Record<keyof T, true> fails to compile if types.ts gains or loses a key,
// and the runtime assertions compare fixture keys to those key sets.
import { describe, expect, it } from 'vitest'
import type {
  Activity, ApiEvent, AuthMe, AuthUser, Comment, CommentAuthor, ErrorBody, Label, Member, MoveResult,
  Project, ProjectDetail, Ticket, TicketSummary, Token,
} from '@/api/types'
import ticket from './fixtures/ticket.json'
import ticketSummary from './fixtures/ticket-summary.json'
import moveResult from './fixtures/move-result.json'
import authUser from './fixtures/auth-user.json'
import authMe from './fixtures/auth-me.json'
import projectDetail from './fixtures/project-detail.json'
import member from './fixtures/member.json'
import label from './fixtures/label.json'
import commentBot from './fixtures/comment-bot.json'
import commentUser from './fixtures/comment-user.json'
import activity from './fixtures/activity.json'
import token from './fixtures/token.json'
import envelope from './fixtures/envelope.json'
import error422 from './fixtures/error-422.json'
import event from './fixtures/event.json'

type Keys<T> = Record<keyof T, true>
const ticketSummaryKeys: Keys<TicketSummary> = {
  id: true, ref: true, project_id: true, project_key: true, number: true, title: true, status: true,
  priority: true, assignee: true, labels: true, position: true, due_date: true, comment_count: true,
  created_at: true, updated_at: true,
}
const ticketKeys: Keys<Ticket> = { ...ticketSummaryKeys, description: true }
const moveKeys: Keys<MoveResult> = { ...ticketKeys, renumbered: true }
const authUserKeys: Keys<AuthUser> = { id: true, email: true, display_name: true, created_at: true }
const authMeKeys: Keys<AuthMe> = { user: true, auth: true }
const projectKeys: Keys<Project> = {
  id: true, key: true, name: true, description: true, archived_at: true, created_at: true,
  updated_at: true, role: true,
}
const projectDetailKeys: Keys<ProjectDetail> = { ...projectKeys, counts: true }
const memberKeys: Keys<Member> = { user: true, role: true, created_at: true }
const labelKeys: Keys<Label> = { id: true, project_id: true, name: true, color: true }
const commentKeys: Keys<Comment> = { id: true, ticket_id: true, author: true, body: true, created_at: true, edited_at: true }
const activityKeys: Keys<Activity> = { id: true, ticket_id: true, actor: true, action: true, changes: true, created_at: true }
const tokenKeys: Keys<Token> = {
  id: true, name: true, token_prefix: true, scope: true, project: true, last_used_at: true,
  revoked_at: true, created_at: true,
}
const authorKeysBase: Omit<Keys<CommentAuthor>, 'owner_name'> = { type: true, id: true, name: true, bot: true }
const eventRequired: Pick<Keys<ApiEvent>, 'type' | 'project_id' | 'actor' | 'at'> = {
  type: true, project_id: true, actor: true, at: true,
}

const sorted = (o: object) => Object.keys(o).sort()

describe('fixtures match types.ts', () => {
  it('ticket shapes', () => {
    expect(sorted(ticketSummary)).toEqual(sorted(ticketSummaryKeys))
    expect(sorted(ticket)).toEqual(sorted(ticketKeys))
    expect(sorted(moveResult)).toEqual(sorted(moveKeys))
    expect('description' in ticketSummary).toBe(false)
    expect(moveResult.renumbered).toBe(false)
  })
  it('auth and project shapes', () => {
    expect(sorted(authUser)).toEqual(sorted(authUserKeys))
    expect(sorted(authMe)).toEqual(sorted(authMeKeys))
    expect(sorted(projectDetail)).toEqual(sorted(projectDetailKeys))
    expect(sorted(projectDetail.counts)).toEqual(['backlog', 'done', 'in_progress', 'todo'])
  })
  it('member, label, token', () => {
    expect(sorted(member)).toEqual(sorted(memberKeys))
    expect(sorted(member.user)).toEqual(['display_name', 'email', 'id'])
    expect(sorted(label)).toEqual(sorted(labelKeys))
    expect(sorted(token)).toEqual(sorted(tokenKeys))
    expect(sorted(token.project)).toEqual(['id', 'key'])
  })
  it('comments and activity', () => {
    expect(sorted(commentBot)).toEqual(sorted(commentKeys))
    expect(sorted(commentUser)).toEqual(sorted(commentKeys))
    expect(sorted(activity)).toEqual(sorted(activityKeys))
    expect(sorted(commentBot.author)).toEqual([...sorted(authorKeysBase), 'owner_name'].sort())
    expect(sorted(commentUser.author)).toEqual(sorted(authorKeysBase))
    expect(commentBot.author.bot).toBe(true)
  })
  it('envelope, error, event', () => {
    expect(sorted(envelope)).toEqual(['items', 'next_cursor'])
    const e: ErrorBody = error422
    expect(e.error.fields?.title).toBeTruthy()
    for (const k of Object.keys(eventRequired)) expect(k in event).toBe(true)
    expect(sorted(event.actor)).toEqual(['id', 'type'])
  })
})
