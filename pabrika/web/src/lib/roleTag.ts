export const TAGGED_ROLES = ['orchestrator', 'researcher', 'developer', 'reviewer', 'qa'] as const
export type TaggedRole = (typeof TAGGED_ROLES)[number]
export type TagTone = 'green' | 'orange' | 'red' | 'neutral'

export const ROLE_LABEL: Record<TaggedRole, string> = {
  orchestrator: 'Orchestrator',
  researcher: 'Researcher',
  developer: 'Developer',
  reviewer: 'Reviewer',
  qa: 'QA',
}
const TONE: Record<string, TagTone> = {
  APPROVE: 'green',
  PASS: 'green',
  'CHANGES REQUESTED': 'orange',
  FAIL: 'orange',
  BLOCKED: 'red',
}

// "[role] TYPE": the type is the leading uppercase words and must not run into a lowercase letter.
const TAG = /^\[(orchestrator|researcher|developer|reviewer|qa)\][ \t]+([A-Z][A-Z ]{1,30})(?![A-Za-z])/

export interface RoleTag {
  role: TaggedRole
  roleLabel: string
  /** Upper case message type, for example `CHANGES REQUESTED`. */
  type: string
  /** Sentence case message type for display. */
  typeLabel: string
  tone: TagTone
  /** Body to render: without the tag line when it is exactly the first line, else the original body. */
  text: string
}

/** Parses the `[role] TYPE` tag. Only bot (API token) authors count, so a human cannot spoof a role. */
export function parseRoleTag(body: string, author: { bot: boolean }): RoleTag | null {
  if (!author.bot) return null
  const m = TAG.exec(body)
  if (!m) return null
  const type = m[2].trim()
  const nl = body.indexOf('\n')
  const firstLine = nl < 0 ? body : body.slice(0, nl)
  const tagOnly = firstLine.trimEnd() === m[0].trimEnd()
  const text = tagOnly ? (nl < 0 ? '' : body.slice(nl + 1)) : body
  const role = m[1] as TaggedRole
  return {
    role,
    roleLabel: ROLE_LABEL[role],
    type,
    typeLabel: type.charAt(0) + type.slice(1).toLowerCase(),
    tone: TONE[type] ?? 'neutral',
    text,
  }
}
