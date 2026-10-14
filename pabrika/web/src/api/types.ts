// DTOs mirroring Phase 2 section 6 exactly. Ticket key is `ref` (never `reference`).

export const STATUSES = ['backlog', 'todo', 'in_progress', 'done'] as const
export type Status = (typeof STATUSES)[number]
export const PRIORITIES = ['low', 'medium', 'high', 'urgent'] as const
export type Priority = (typeof PRIORITIES)[number]
export const ROLES = ['owner', 'editor', 'viewer'] as const
export type Role = (typeof ROLES)[number]
export const SCOPES = ['read', 'comment', 'write'] as const
export type Scope = (typeof SCOPES)[number]
export const LABEL_COLORS = [
  'gray', 'red', 'orange', 'amber', 'green', 'teal', 'blue', 'indigo', 'purple', 'pink',
] as const
export type LabelColor = (typeof LABEL_COLORS)[number]

export interface User {
  id: string
  email: string
  display_name: string
}

export interface AuthUser extends User {
  created_at: string
}

export interface AuthMe {
  user: AuthUser
  auth: { method: 'session' | 'token'; token?: unknown }
}

export interface AuthConfig {
  signup_enabled: boolean
}

export interface Project {
  id: string
  key: string
  name: string
  description: string
  archived_at: string | null
  created_at: string
  updated_at: string
  role: Role
}

export interface ProjectCounts {
  backlog: number
  todo: number
  in_progress: number
  done: number
}

export interface ProjectDetail extends Project {
  counts: ProjectCounts
}

export interface Member {
  user: User
  role: Role
  created_at: string
}

export interface Label {
  id: string
  project_id: string
  name: string
  color: LabelColor | string
}

export interface Assignee {
  id: string
  display_name: string
  email: string
}

export const GATES = ['requirements', 'plan'] as const
export type Gate = (typeof GATES)[number]
export const VERDICTS = ['approved', 'changes_requested'] as const
export type Verdict = (typeof VERDICTS)[number]

export interface Round {
  n: number
  max: number
}

/** Latest decision per gate, ticket detail only (workflow.decisions). */
export interface WorkflowDecision {
  gate: Gate
  verdict: Verdict
  note: string | null
  at: string
  by: { id: string; name: string }
  current?: boolean
}

/** Derived by the server from description lines and bot comments; null when everything is empty. */
export interface Workflow {
  pr_url: string | null
  branch: string | null
  round: Round | null
  decisions?: WorkflowDecision[]
}

export interface Decision {
  id: string
  ticket_id: string
  gate: Gate
  verdict: Verdict
  note: string | null
  actor: { id: string; name: string }
  actor_role: string
  gate_label: string
  created_at: string
}

export interface CreateDecisionInput {
  gate: Gate
  verdict: Verdict
  note?: string
}

export type FeedKind = 'activity' | 'comment' | 'decision'

export interface ActivityFeedItem {
  id: string
  kind: FeedKind
  ticket: { id: string; ref: string; title: string }
  actor: CommentAuthor
  action?: string
  changes?: Record<string, unknown>
  /** Comment body, cut to 200 characters by the server. */
  summary?: string
  created_at: string
}

export interface TicketSummary {
  id: string
  ref: string
  project_id: string
  project_key: string
  number: number
  title: string
  status: Status
  priority: Priority
  assignee: Assignee | null
  labels: Label[]
  position: number
  due_date: string | null
  comment_count: number
  /** Absent on servers older than agentic phase 4. */
  workflow?: Workflow | null
  created_at: string
  updated_at: string
}

export interface Ticket extends TicketSummary {
  description: string
}

export interface MoveResult extends Ticket {
  renumbered: boolean
}

export interface MoveBody {
  status: Status
  before?: string
  after?: string
  place?: 'top' | 'bottom'
}

export interface CommentAuthor {
  type: 'user' | 'api_token'
  id: string
  name: string
  bot: boolean
  owner_name?: string
}

export interface Comment {
  id: string
  ticket_id: string
  author: CommentAuthor
  body: string
  created_at: string
  edited_at: string | null
}

export interface Activity {
  id: string
  ticket_id: string
  actor: CommentAuthor
  action: string
  changes: Record<string, unknown>
  created_at: string
}

export interface Token {
  id: string
  name: string
  token_prefix: string
  scope: Scope
  project: { id: string; key: string } | null
  last_used_at: string | null
  revoked_at: string | null
  created_at: string
}

export interface TokenCreated {
  token: Token
  secret: string
}

export interface Envelope<T> {
  items: T[]
  next_cursor: string | null
}

export interface ErrorBody {
  error: { code: string; message: string; fields?: Record<string, string> }
}

export const EVENT_TYPES = [
  'ticket.created',
  'ticket.updated',
  'ticket.moved',
  'ticket.deleted',
  'comment.added',
  'comment.changed',
  'label.changed',
  'member.changed',
  'project.updated',
  'decision.recorded',
] as const
export type EventType = (typeof EVENT_TYPES)[number]

export interface ApiEvent {
  type: EventType
  project_id: string
  ticket_id?: string
  comment_id?: string
  decision_id?: string
  label_id?: string
  user_id?: string
  renumbered?: boolean
  actor: { type: 'user' | 'api_token'; id: string }
  at: string
}

// Request bodies
export interface CreateProjectInput { key: string; name: string; description?: string }
export interface UpdateProjectInput { name?: string; description?: string; archived?: boolean }
export interface CreateTicketInput { title: string; status?: Status }
export interface UpdateTicketInput {
  title?: string
  description?: string
  priority?: Priority
  assignee?: string | null
  labels?: string[]
  due_date?: string | null
}
export interface CreateTokenInput { name: string; scope: Scope; project_id?: string }
