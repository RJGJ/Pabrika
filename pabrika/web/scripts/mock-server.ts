// In-memory mock of the Phase 2/3 API for UI development: `bun run mock` (port 8080).
// Seed users (mock only): ada@example.com / grace@example.com, password "mock-password-1".
// Hooks: POST /__mock/emit {project, type, ticket_id?, actor?}  -> broadcast an SSE event (as an agent would)
//        POST /__mock/drop                                       -> close all SSE streams (forces reconnect)
//        POST /__mock/config {renumber?, allow_signup?}          -> toggle behaviour
// Env: PORT (8080), MOCK_ORIGIN (http://localhost:5173), ALLOW_SIGNUP (true|false)
import { createServer, type IncomingMessage, type ServerResponse } from 'node:http'
import { randomBytes } from 'node:crypto'

const PORT = Number(process.env.PORT ?? 8080)
const ORIGIN = process.env.MOCK_ORIGIN ?? 'http://localhost:5173'
const cfg = { renumber: false, allow_signup: process.env.ALLOW_SIGNUP !== 'false' }

type Any = Record<string, any>
let seq = 0
const ulid = () => `01MOCK${Date.now().toString(36).toUpperCase()}${(seq++).toString(36).toUpperCase().padStart(6, '0')}`
const now = () => new Date().toISOString()

interface UserRec { id: string; email: string; display_name: string; password: string; created_at: string }
const users: UserRec[] = []
const sessions = new Map<string, string>() // session id -> user id
const projects: Any[] = []
const members = new Map<string, { user_id: string; role: string; created_at: string }[]>() // project id
const labels: Any[] = []
const tickets: Any[] = [] // full tickets, with `labelIds`, `assigneeId`, `deleted`
const comments: Any[] = []
const activity: Any[] = []
const tokens: Any[] = []
const streams = new Map<string, Set<ServerResponse>>() // project id -> responses

function seed() {
  users.length = projects.length = labels.length = tickets.length = comments.length = activity.length = tokens.length = 0
  members.clear()
  const ada = { id: ulid(), email: 'ada@example.com', display_name: 'Ada Lovelace', password: 'mock-password-1', created_at: now() }
  const grace = { id: ulid(), email: 'grace@example.com', display_name: 'Grace Hopper', password: 'mock-password-1', created_at: now() }
  users.push(ada, grace)
  const p = { id: ulid(), key: 'WEB', name: 'Website', description: 'The public website', archived_at: null, created_at: now(), updated_at: now(), next: 1 }
  projects.push(p)
  members.set(p.id, [
    { user_id: ada.id, role: 'owner', created_at: now() },
    { user_id: grace.id, role: 'viewer', created_at: now() },
  ])
  const bug = { id: ulid(), project_id: p.id, name: 'bug', color: 'red' }
  const ui = { id: ulid(), project_id: p.id, name: 'ui', color: 'blue' }
  labels.push(bug, ui)
  const mk = (title: string, status: string, priority: string, extra: Any = {}) => addTicket(p, title, status, { priority, ...extra })
  mk('Fix login redirect', 'todo', 'high', { labelIds: [bug.id], assigneeId: ada.id, due_date: '2026-10-01' })
  mk('Add dark mode', 'backlog', 'medium', { labelIds: [ui.id] })
  mk('Write README', 'in_progress', 'low', { assigneeId: grace.id })
  mk('Set up CI', 'done', 'urgent')
}

function addTicket(p: Any, title: string, status: string, extra: Any = {}) {
  const inCol = tickets.filter((t) => t.project_id === p.id && t.status === status && !t.deleted)
  const last = inCol.reduce((m, t) => Math.max(m, t.position), 0)
  const t = {
    id: ulid(), project_id: p.id, project_key: p.key, number: p.next++, title, description: '', status,
    priority: 'medium', labelIds: [] as string[], assigneeId: null as string | null, position: last + 1024,
    due_date: null as string | null, created_at: now(), updated_at: now(), deleted: false, ...extra,
  }
  tickets.push(t)
  return t
}

// ---------- helpers ----------
const json = (res: ServerResponse, status: number, body?: unknown, headers: Record<string, string> = {}) => {
  res.writeHead(status, { 'Content-Type': 'application/json', ...headers })
  res.end(status === 204 || body === undefined ? undefined : JSON.stringify(body))
}
const fail = (res: ServerResponse, status: number, code: string, message: string, fields?: Record<string, string>) =>
  json(res, status, { error: { code, message, ...(fields ? { fields } : {}) } })
const envelope = (items: unknown[]) => ({ items, next_cursor: null })

const userDto = (u: UserRec) => ({ id: u.id, email: u.email, display_name: u.display_name })
const authUserDto = (u: UserRec) => ({ ...userDto(u), created_at: u.created_at })
const findUser = (id: string | null) => users.find((u) => u.id === id)
const labelDto = (l: Any) => ({ id: l.id, project_id: l.project_id, name: l.name, color: l.color })

function roleOf(p: Any, userId: string): string | null {
  return members.get(p.id)?.find((m) => m.user_id === userId)?.role ?? null
}
function findProject(keyOrId: string, userId: string): Any | null {
  const p = projects.find((x) => x.id === keyOrId || x.key === keyOrId.toUpperCase())
  return p && roleOf(p, userId) ? p : null
}
const projectDto = (p: Any, userId: string) => ({
  id: p.id, key: p.key, name: p.name, description: p.description, archived_at: p.archived_at,
  created_at: p.created_at, updated_at: p.updated_at, role: roleOf(p, userId),
})
function ticketSummary(t: Any) {
  const a = findUser(t.assigneeId)
  return {
    id: t.id, ref: `${t.project_key}-${t.number}`, project_id: t.project_id, project_key: t.project_key, number: t.number,
    title: t.title, status: t.status, priority: t.priority,
    assignee: a ? { id: a.id, display_name: a.display_name, email: a.email } : null,
    labels: t.labelIds.map((id: string) => labels.find((l) => l.id === id)).filter(Boolean).map(labelDto),
    position: t.position, due_date: t.due_date,
    comment_count: comments.filter((c) => c.ticket_id === t.id).length,
    created_at: t.created_at, updated_at: t.updated_at,
  }
}
const ticketFull = (t: Any) => ({ ...ticketSummary(t), description: t.description })
const findTicket = (idOrRef: string, userId: string): Any | null => {
  const t = tickets.find((x) => !x.deleted && (x.id === idOrRef || `${x.project_key}-${x.number}` === idOrRef.toUpperCase()))
  if (!t) return null
  const p = projects.find((x) => x.id === t.project_id)
  return p && roleOf(p, userId) ? t : null
}
const authorDto = (a: Any) => a // stored in DTO form

function log(t: Any, actor: Any, action: string, changes: Any) {
  activity.push({ id: ulid(), ticket_id: t.id, actor, action, changes, created_at: now() })
}
const userActor = (u: UserRec) => ({ type: 'user', id: u.id, name: u.display_name, bot: false })

function emit(projectId: string, type: string, extra: Any = {}, actor: Any = { type: 'user', id: 'system' }) {
  const payload = JSON.stringify({ type, project_id: projectId, ...extra, actor: { type: actor.type, id: actor.id }, at: now() })
  for (const res of streams.get(projectId) ?? []) res.write(`event: ${type}\ndata: ${payload}\n\n`)
}

const bodyCache = new WeakMap<IncomingMessage, Any>()
async function readBody(req: IncomingMessage): Promise<Any> {
  const cached = bodyCache.get(req)
  if (cached) return cached
  const b = await readRaw(req)
  bodyCache.set(req, b)
  return b
}
async function readRaw(req: IncomingMessage): Promise<Any> {
  const chunks: Buffer[] = []
  for await (const c of req) chunks.push(c as Buffer)
  const text = Buffer.concat(chunks).toString('utf8')
  if (!text) return {}
  try {
    return JSON.parse(text)
  } catch {
    return { __invalid: true }
  }
}

function sessionUser(req: IncomingMessage): UserRec | null {
  const m = /(?:^|;\s*)pb_session=([^;]+)/.exec(req.headers.cookie ?? '')
  const uid = m ? sessions.get(m[1]) : undefined
  return uid ? (findUser(uid) ?? null) : null
}

function paginate(items: any[], url: URL, newestFirst = false) {
  const limit = Math.min(Number(url.searchParams.get('limit') ?? 50) || 50, 200)
  const cursor = url.searchParams.get('cursor')
  let start = 0
  if (cursor) {
    start = Number(cursor.replace('c', ''))
    if (!Number.isInteger(start) || start < 0) return null
  }
  const list = newestFirst ? [...items].reverse() : items
  const page = list.slice(start, start + limit)
  return { items: page, next_cursor: start + limit < list.length ? `c${start + limit}` : null }
}

function placeTicket(t: Any, status: string, body: Any): { ok: true; renumbered: boolean } | { ok: false; msg: string } {
  const col = tickets.filter((x) => x.project_id === t.project_id && x.status === status && !x.deleted && x.id !== t.id)
    .sort((a, b) => a.position - b.position)
  let renumbered = false
  let pos: number
  const neighbour = (id: string) => col.findIndex((x) => x.id === id)
  if (body.after) {
    const i = neighbour(body.after)
    if (i < 0) return { ok: false, msg: 'after must be a ticket in the target column' }
    pos = i + 1 < col.length ? (col[i].position + col[i + 1].position) / 2 : col[i].position + 1024
  } else if (body.before) {
    const i = neighbour(body.before)
    if (i < 0) return { ok: false, msg: 'before must be a ticket in the target column' }
    pos = i > 0 ? (col[i - 1].position + col[i].position) / 2 : col[i].position / 2
  } else if (body.place === 'top') {
    pos = col.length ? col[0].position / 2 : 1024
  } else if (body.place === 'bottom') {
    pos = col.length ? col[col.length - 1].position + 1024 : 1024
  } else return { ok: false, msg: 'one of before, after or place is required' }
  t.status = status
  t.position = pos
  if (cfg.renumber) {
    renumbered = true
    cfg.renumber = false
    const ordered = [...col, t].sort((a, b) => a.position - b.position)
    ordered.forEach((x, i) => (x.position = (i + 1) * 1024))
  }
  return { ok: true, renumbered }
}

// ---------- routing ----------
async function handle(req: IncomingMessage, res: ServerResponse) {
  const url = new URL(req.url ?? '/', 'http://mock')
  const method = req.method ?? 'GET'
  const path = url.pathname

  if (path.startsWith('/__mock/')) {
    const body = await readBody(req)
    if (path === '/__mock/emit') {
      const p = projects.find((x) => x.key === body.project || x.id === body.project)
      if (!p) return fail(res, 404, 'not_found', 'no such project')
      emit(p.id, body.type ?? 'ticket.updated', { ticket_id: body.ticket_id, renumbered: body.renumbered, user_id: body.user_id },
        body.actor ?? { type: 'api_token', id: 'mock-token' })
      return json(res, 200, { ok: true })
    }
    if (path === '/__mock/drop') {
      for (const set of streams.values()) for (const r of set) r.end()
      streams.clear()
      return json(res, 200, { ok: true })
    }
    if (path === '/__mock/config') {
      Object.assign(cfg, body)
      return json(res, 200, cfg)
    }
    if (path === '/__mock/reset') {
      seed()
      return json(res, 200, { ok: true })
    }
    return fail(res, 404, 'not_found', 'not found')
  }

  if (!path.startsWith('/api/v1/')) return fail(res, 404, 'not_found', 'not found')
  const p = path.slice('/api/v1'.length)

  // Origin check (as the real server does) for mutations.
  if (['POST', 'PATCH', 'DELETE', 'PUT'].includes(method)) {
    const origin = req.headers.origin
    if (origin && origin !== ORIGIN) return fail(res, 403, 'origin_mismatch', 'Origin does not match BASE_URL')
  }

  const m = (re: RegExp) => re.exec(p)
  let r: RegExpExecArray | null

  // ----- public auth -----
  if (method === 'GET' && p === '/auth/config') return json(res, 200, { signup_enabled: cfg.allow_signup })
  if (method === 'POST' && p === '/auth/signup') {
    if (!cfg.allow_signup) return fail(res, 404, 'not_found', 'not found')
    const b = await readBody(req)
    const fields: Record<string, string> = {}
    if (!/^\S+@\S+\.\S+$/.test(b.email ?? '')) fields.email = 'Enter a valid email'
    if (!b.display_name || b.display_name.length > 100) fields.display_name = 'Display name must be 1 to 100 characters'
    if (!b.password || b.password.length < 10) fields.password = 'Password must be at least 10 characters'
    if (Object.keys(fields).length) return fail(res, 422, 'validation_failed', 'Invalid input', fields)
    if (users.some((u) => u.email === b.email.toLowerCase())) return fail(res, 409, 'email_taken', 'An account with this email already exists')
    const u: UserRec = { id: ulid(), email: b.email.toLowerCase(), display_name: b.display_name, password: b.password, created_at: now() }
    users.push(u)
    const sid = randomBytes(16).toString('hex')
    sessions.set(sid, u.id)
    return json(res, 201, { user: authUserDto(u) }, { 'Set-Cookie': `pb_session=${sid}; Path=/; HttpOnly; SameSite=Lax` })
  }
  if (method === 'POST' && p === '/auth/login') {
    const b = await readBody(req)
    const u = users.find((x) => x.email === String(b.email ?? '').toLowerCase())
    if (!u || u.password !== b.password) return fail(res, 401, 'invalid_credentials', 'Invalid email or password')
    const sid = randomBytes(16).toString('hex')
    sessions.set(sid, u.id)
    return json(res, 200, { user: authUserDto(u) }, { 'Set-Cookie': `pb_session=${sid}; Path=/; HttpOnly; SameSite=Lax` })
  }

  // ----- everything below needs a session -----
  const me = sessionUser(req)
  if (!me) return fail(res, 401, 'unauthorized', 'Authentication required')

  if (method === 'POST' && p === '/auth/logout') {
    const sid = /pb_session=([^;]+)/.exec(req.headers.cookie ?? '')?.[1]
    if (sid) sessions.delete(sid)
    return json(res, 204, undefined, { 'Set-Cookie': 'pb_session=; Path=/; Max-Age=0' })
  }
  if (method === 'GET' && p === '/auth/me') return json(res, 200, { user: authUserDto(me), auth: { method: 'session' } })
  if (method === 'PATCH' && p === '/auth/me') {
    const b = await readBody(req)
    const name = String(b.display_name ?? '').trim()
    if (!name || name.length > 100) return fail(res, 422, 'validation_failed', 'Invalid input', { display_name: 'Display name must be 1 to 100 characters' })
    me.display_name = name
    return json(res, 200, { user: authUserDto(me) })
  }
  if (method === 'POST' && p === '/auth/me/password') {
    const b = await readBody(req)
    if (b.current_password !== me.password) return fail(res, 422, 'validation_failed', 'Invalid input', { current_password: 'Incorrect password' })
    if (!b.new_password || b.new_password.length < 10) return fail(res, 422, 'validation_failed', 'Invalid input', { new_password: 'Password must be at least 10 characters' })
    me.password = b.new_password
    const keep = /pb_session=([^;]+)/.exec(req.headers.cookie ?? '')?.[1]
    for (const [sid, uid] of sessions) if (uid === me.id && sid !== keep) sessions.delete(sid)
    return json(res, 204)
  }

  // ----- tokens -----
  if (p === '/tokens' && method === 'GET') return json(res, 200, envelope([...tokens].filter((t) => t.owner === me.id).reverse().map(({ owner: _o, ...t }) => t)))
  if (p === '/tokens' && method === 'POST') {
    const b = await readBody(req)
    if (!b.name || !['read', 'write'].includes(b.scope)) return fail(res, 422, 'validation_failed', 'Invalid input', { name: 'Name and scope are required' })
    const proj = b.project_id ? projects.find((x) => x.id === b.project_id) : null
    const secret = 'pb_' + randomBytes(24).toString('hex')
    const t = { id: ulid(), owner: me.id, name: b.name, token_prefix: secret.slice(0, 9), scope: b.scope,
      project: proj ? { id: proj.id, key: proj.key } : null, last_used_at: null, revoked_at: null, created_at: now() }
    tokens.push(t)
    const { owner: _o, ...dto } = t
    return json(res, 201, { token: dto, secret })
  }
  if ((r = m(/^\/tokens\/([^/]+)$/)) && method === 'DELETE') {
    const t = tokens.find((x) => x.id === r![1] && x.owner === me.id)
    if (!t) return fail(res, 404, 'not_found', 'not found')
    t.revoked_at ??= now()
    return json(res, 204)
  }

  // ----- projects -----
  if (p === '/projects' && method === 'GET') {
    const incl = url.searchParams.get('archived') === 'true'
    const items = projects.filter((x) => roleOf(x, me.id) && (incl || !x.archived_at))
      .sort((a, b) => a.name.localeCompare(b.name) || a.key.localeCompare(b.key))
      .map((x) => projectDto(x, me.id))
    return json(res, 200, envelope(items))
  }
  if (p === '/projects' && method === 'POST') {
    const b = await readBody(req)
    const fields: Record<string, string> = {}
    const key = String(b.key ?? '').toUpperCase()
    if (!/^[A-Z]{2,6}$/.test(key)) fields.key = 'Key must be 2 to 6 letters'
    if (!b.name || b.name.length > 100) fields.name = 'Name must be 1 to 100 characters'
    if ((b.description ?? '').length > 2000) fields.description = 'Description is too long'
    if (Object.keys(fields).length) return fail(res, 422, 'validation_failed', 'Invalid input', fields)
    if (projects.some((x) => x.key === key)) return fail(res, 409, 'key_taken', 'A project with this key already exists')
    const proj = { id: ulid(), key, name: b.name, description: b.description ?? '', archived_at: null, created_at: now(), updated_at: now(), next: 1 }
    projects.push(proj)
    members.set(proj.id, [{ user_id: me.id, role: 'owner', created_at: now() }])
    return json(res, 201, projectDto(proj, me.id))
  }
  if ((r = m(/^\/projects\/([^/]+)$/))) {
    const proj = findProject(r[1], me.id)
    if (!proj) return fail(res, 404, 'not_found', 'Project not found')
    const role = roleOf(proj, me.id)
    if (method === 'GET') {
      const counts = { backlog: 0, todo: 0, in_progress: 0, done: 0 } as Any
      for (const t of tickets) if (t.project_id === proj.id && !t.deleted) counts[t.status]++
      return json(res, 200, { ...projectDto(proj, me.id), counts })
    }
    if (method === 'PATCH') {
      if (role !== 'owner') return fail(res, 403, 'forbidden', 'Only owners can edit the project')
      const b = await readBody(req)
      if (typeof b.name === 'string') proj.name = b.name
      if (typeof b.description === 'string') proj.description = b.description
      if (typeof b.archived === 'boolean') proj.archived_at = b.archived ? now() : null
      proj.updated_at = now()
      emit(proj.id, 'project.updated', {}, { type: 'user', id: me.id })
      return json(res, 200, projectDto(proj, me.id))
    }
    if (method === 'DELETE') {
      if (role !== 'owner') return fail(res, 403, 'forbidden', 'Only owners can delete the project')
      projects.splice(projects.indexOf(proj), 1)
      for (const t of tickets) if (t.project_id === proj.id) t.deleted = true
      for (const s of streams.get(proj.id) ?? []) s.end()
      streams.delete(proj.id)
      return json(res, 204)
    }
  }

  // ----- project children -----
  if ((r = m(/^\/projects\/([^/]+)\/(members|labels|tickets|events)(?:\/([^/]+))?$/))) {
    const proj = findProject(r[1], me.id)
    if (!proj) return fail(res, 404, 'not_found', 'Project not found')
    const role = roleOf(proj, me.id)!
    const canWrite = (role === 'owner' || role === 'editor')
    const archivedBlock = () => (proj.archived_at ? fail(res, 409, 'project_archived', 'Project is archived') : null)
    const kind = r[2]
    const sub = r[3]

    if (kind === 'events' && method === 'GET' && !sub) {
      res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' })
      res.write('retry: 3000\n\n')
      let set = streams.get(proj.id)
      if (!set) streams.set(proj.id, (set = new Set()))
      set.add(res)
      const ka = setInterval(() => res.write(': keepalive\n\n'), 25000)
      req.on('close', () => {
        clearInterval(ka)
        set!.delete(res)
      })
      return
    }

    if (kind === 'members') {
      const list = members.get(proj.id)!
      if (method === 'GET' && !sub) {
        const items = [...list]
          .sort((a, b) => Number(b.role === 'owner') - Number(a.role === 'owner') || findUser(a.user_id)!.display_name.localeCompare(findUser(b.user_id)!.display_name))
          .map((x) => ({ user: userDto(findUser(x.user_id)!), role: x.role, created_at: x.created_at }))
        return json(res, 200, envelope(items))
      }
      if (method === 'POST' && !sub) {
        if (role !== 'owner') return fail(res, 403, 'forbidden', 'Only owners can manage members')
        const b = await readBody(req)
        const u = users.find((x) => x.email === String(b.email ?? '').toLowerCase())
        if (!u) return fail(res, 422, 'validation_failed', 'Invalid input', { email: 'No account with this email' })
        if (list.some((x) => x.user_id === u.id)) return fail(res, 409, 'already_member', 'Already a member')
        const rec = { user_id: u.id, role: b.role ?? 'viewer', created_at: now() }
        list.push(rec)
        emit(proj.id, 'member.changed', { user_id: u.id }, { type: 'user', id: me.id })
        return json(res, 201, { user: userDto(u), role: rec.role, created_at: rec.created_at })
      }
      if (sub && (method === 'PATCH' || method === 'DELETE')) {
        const rec = list.find((x) => x.user_id === sub)
        if (!rec) return fail(res, 404, 'not_found', 'Member not found')
        if (role !== 'owner' && !(method === 'DELETE' && sub === me.id)) return fail(res, 403, 'forbidden', 'Only owners can manage members')
        const owners = list.filter((x) => x.role === 'owner')
        if (rec.role === 'owner' && owners.length === 1 && (method === 'DELETE' || (await peek(req)).role !== 'owner')) {
          return fail(res, 409, 'last_owner', 'A project needs at least one owner')
        }
        if (method === 'PATCH') {
          const b = await readBody(req)
          rec.role = b.role
          emit(proj.id, 'member.changed', { user_id: sub }, { type: 'user', id: me.id })
          return json(res, 200, { user: userDto(findUser(sub)!), role: rec.role, created_at: rec.created_at })
        }
        list.splice(list.indexOf(rec), 1)
        for (const t of tickets) if (t.project_id === proj.id && t.assigneeId === sub) t.assigneeId = null
        emit(proj.id, 'member.changed', { user_id: sub }, { type: 'user', id: me.id })
        for (const s of streams.get(proj.id) ?? []) s.end()
        streams.delete(proj.id)
        return json(res, 204)
      }
    }

    if (kind === 'labels' && !sub) {
      if (method === 'GET') return json(res, 200, envelope(labels.filter((l) => l.project_id === proj.id).sort((a, b) => a.name.localeCompare(b.name)).map(labelDto)))
      if (method === 'POST') {
        if (!canWrite) return fail(res, 403, 'forbidden', 'Not allowed')
        const blocked = archivedBlock()
        if (blocked) return
        const b = await readBody(req)
        if (!b.name || b.name.length > 50) return fail(res, 422, 'validation_failed', 'Invalid input', { name: 'Name must be 1 to 50 characters' })
        if (labels.some((l) => l.project_id === proj.id && l.name.toLowerCase() === b.name.toLowerCase())) return fail(res, 409, 'label_exists', 'A label with this name exists')
        const l = { id: ulid(), project_id: proj.id, name: b.name, color: b.color ?? 'gray' }
        labels.push(l)
        emit(proj.id, 'label.changed', { label_id: l.id }, { type: 'user', id: me.id })
        return json(res, 201, labelDto(l))
      }
    }

    if (kind === 'tickets' && !sub) {
      if (method === 'GET') {
        const all = tickets.filter((t) => t.project_id === proj.id && !t.deleted).sort((a, b) => a.position - b.position || a.id.localeCompare(b.id))
        const page = paginate(all, url)
        if (!page) return fail(res, 400, 'invalid_cursor', 'Invalid cursor')
        return json(res, 200, { items: page.items.map((t) => { const { description: _d, ...s } = ticketFull(t); return s }), next_cursor: page.next_cursor })
      }
      if (method === 'POST') {
        if (!canWrite) return fail(res, 403, 'forbidden', 'Not allowed')
        if (archivedBlock()) return
        const b = await readBody(req)
        const title = String(b.title ?? '').trim()
        if (!title || title.length > 200) return fail(res, 422, 'validation_failed', 'Invalid input', { title: 'Title must be 1 to 200 characters' })
        const t = addTicket(proj, title, b.status ?? 'backlog')
        log(t, userActor(me), 'created', {})
        emit(proj.id, 'ticket.created', { ticket_id: t.id }, { type: 'user', id: me.id })
        return json(res, 201, ticketFull(t))
      }
    }
  }

  // ----- labels by id -----
  if ((r = m(/^\/labels\/([^/]+)$/))) {
    const l = labels.find((x) => x.id === r![1])
    const proj = l && projects.find((x) => x.id === l.project_id)
    if (!l || !proj || !roleOf(proj, me.id)) return fail(res, 404, 'not_found', 'not found')
    const role = roleOf(proj, me.id)
    if (role === 'viewer') return fail(res, 403, 'forbidden', 'Not allowed')
    if (proj.archived_at) return fail(res, 409, 'project_archived', 'Project is archived')
    if (method === 'PATCH') {
      const b = await readBody(req)
      if (typeof b.name === 'string') l.name = b.name
      if (typeof b.color === 'string') l.color = b.color
    } else if (method === 'DELETE') {
      labels.splice(labels.indexOf(l), 1)
      for (const t of tickets) t.labelIds = t.labelIds.filter((id: string) => id !== l.id)
    }
    emit(proj.id, 'label.changed', { label_id: l.id }, { type: 'user', id: me.id })
    return method === 'DELETE' ? json(res, 204) : json(res, 200, labelDto(l))
  }

  // ----- tickets by id/ref -----
  if ((r = m(/^\/tickets\/([^/]+)(?:\/(move|comments|activity))?$/))) {
    const t = findTicket(r[1], me.id)
    if (!t) return fail(res, 404, 'not_found', 'Ticket not found')
    const proj = projects.find((x) => x.id === t.project_id)!
    const role = roleOf(proj, me.id)!
    const canWrite = role === 'owner' || role === 'editor'
    const sub = r[2]
    const actor = { type: 'user', id: me.id }

    if (!sub) {
      if (method === 'GET') return json(res, 200, ticketFull(t))
      if (!canWrite) return fail(res, 403, 'forbidden', 'Not allowed')
      if (proj.archived_at) return fail(res, 409, 'project_archived', 'Project is archived')
      if (method === 'PATCH') {
        const b = await readBody(req)
        const changes: Any = {}
        if ('title' in b) {
          const title = String(b.title).trim()
          if (!title || title.length > 200) return fail(res, 422, 'validation_failed', 'Invalid input', { title: 'Title must be 1 to 200 characters' })
          changes.title = [t.title, title]
          t.title = title
        }
        if ('description' in b) { changes.description = [t.description.slice(0, 200), String(b.description).slice(0, 200)]; t.description = b.description }
        if ('priority' in b) { changes.priority = [t.priority, b.priority]; t.priority = b.priority }
        if ('due_date' in b) { changes.due_date = [t.due_date, b.due_date]; t.due_date = b.due_date }
        if ('assignee' in b) { changes.assignee = [t.assigneeId, b.assignee]; t.assigneeId = b.assignee }
        if ('labels' in b) {
          const name = (id: string) => labels.find((l) => l.id === id)?.name
          changes.labels = [t.labelIds.map(name), b.labels.map(name)]
          t.labelIds = b.labels
        }
        t.updated_at = now()
        log(t, userActor(me), 'updated', changes)
        emit(proj.id, 'ticket.updated', { ticket_id: t.id }, actor)
        return json(res, 200, ticketFull(t))
      }
      if (method === 'DELETE') {
        t.deleted = true
        log(t, userActor(me), 'deleted', {})
        emit(proj.id, 'ticket.deleted', { ticket_id: t.id }, actor)
        return json(res, 204)
      }
    }
    if (sub === 'move' && method === 'POST') {
      if (!canWrite) return fail(res, 403, 'forbidden', 'Not allowed')
      if (proj.archived_at) return fail(res, 409, 'project_archived', 'Project is archived')
      const b = await readBody(req)
      if (!['backlog', 'todo', 'in_progress', 'done'].includes(b.status)) return fail(res, 422, 'validation_failed', 'Invalid input', { status: 'Invalid status' })
      const from = t.status
      const out = placeTicket(t, b.status, b)
      if (!out.ok) return fail(res, 422, 'validation_failed', 'Invalid input', { after: out.msg })
      t.updated_at = now()
      log(t, userActor(me), 'moved', from === t.status ? { position: [0, t.position] } : { status: [from, t.status] })
      emit(proj.id, 'ticket.moved', { ticket_id: t.id, renumbered: out.renumbered }, actor)
      return json(res, 200, { ...ticketFull(t), renumbered: out.renumbered })
    }
    if (sub === 'comments') {
      if (method === 'GET') {
        const page = paginate(comments.filter((c) => c.ticket_id === t.id), url)
        if (!page) return fail(res, 400, 'invalid_cursor', 'Invalid cursor')
        return json(res, 200, { items: page.items.map(({ author, ...c }) => ({ ...c, author: authorDto(author) })), next_cursor: page.next_cursor })
      }
      if (method === 'POST') {
        if (!canWrite) return fail(res, 403, 'forbidden', 'Not allowed')
        if (proj.archived_at) return fail(res, 409, 'project_archived', 'Project is archived')
        const b = await readBody(req)
        if (!b.body || b.body.length > 20000) return fail(res, 422, 'validation_failed', 'Invalid input', { body: 'Body must be 1 to 20,000 characters' })
        const c = { id: ulid(), ticket_id: t.id, author: userActor(me), body: b.body, created_at: now(), edited_at: null }
        comments.push(c)
        emit(proj.id, 'comment.added', { ticket_id: t.id, comment_id: c.id }, actor)
        return json(res, 201, c)
      }
    }
    if (sub === 'activity' && method === 'GET') {
      const page = paginate(activity.filter((a) => a.ticket_id === t.id), url, true)
      if (!page) return fail(res, 400, 'invalid_cursor', 'Invalid cursor')
      return json(res, 200, page)
    }
  }

  // ----- comments by id -----
  if ((r = m(/^\/comments\/([^/]+)$/))) {
    const c = comments.find((x) => x.id === r![1])
    const t = c && findTicket(c.ticket_id, me.id)
    if (!c || !t) return fail(res, 404, 'not_found', 'not found')
    const proj = projects.find((x) => x.id === t.project_id)!
    const role = roleOf(proj, me.id)!
    if (proj.archived_at) return fail(res, 409, 'project_archived', 'Project is archived')
    if (method === 'PATCH') {
      if (c.author.id !== me.id || role === 'viewer') return fail(res, 403, 'forbidden', 'You can only edit your own comments')
      const b = await readBody(req)
      if (!b.body || b.body.length > 20000) return fail(res, 422, 'validation_failed', 'Invalid input', { body: 'Body must be 1 to 20,000 characters' })
      c.body = b.body
      c.edited_at = now()
      emit(proj.id, 'comment.changed', { ticket_id: t.id, comment_id: c.id }, { type: 'user', id: me.id })
      return json(res, 200, c)
    }
    if (method === 'DELETE') {
      if (!(c.author.id === me.id && role !== 'viewer') && role !== 'owner') return fail(res, 403, 'forbidden', 'Not allowed')
      comments.splice(comments.indexOf(c), 1)
      emit(proj.id, 'comment.changed', { ticket_id: t.id, comment_id: c.id }, { type: 'user', id: me.id })
      return json(res, 204)
    }
  }

  return fail(res, 404, 'not_found', 'not found')
}

const peek = async (req: IncomingMessage): Promise<Any> => bodyCache.get(req) ?? {}

seed()
createServer((req, res) => {
  // Cache the body up front for write methods so handlers (and peek) can read it repeatedly.
  const prep = ['POST', 'PATCH', 'PUT'].includes(req.method ?? '') ? readBody(req) : Promise.resolve({})
  prep.then(() => handle(req, res)).catch((e) => {
    console.error(e)
    if (!res.headersSent) fail(res, 500, 'internal', 'internal error')
  })
}).listen(PORT, 'localhost', () => console.log(`mock API on http://localhost:${PORT} (origin ${ORIGIN})`))
