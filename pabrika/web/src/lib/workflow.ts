import type { Gate, Round } from '@/api/types'

/** Stage labels in a fixed order: a ticket with two of them shows the first and a conflict hint. */
export const STAGE_LABELS = [
  'needs-requirements',
  'awaiting-req-approval',
  'awaiting-plan-approval',
  'needs-dev',
  'needs-review',
  'needs-qa',
  'awaiting-merge',
] as const
export type StageLabel = (typeof STAGE_LABELS)[number]
export const FLAG_LABELS = ['blocked', 'changes-requested'] as const
export type FlagLabel = (typeof FLAG_LABELS)[number]
export const KIND_LABELS = ['epic', 'task'] as const
export type KindLabel = (typeof KIND_LABELS)[number]

const STAGE_TEXT: Record<StageLabel, string> = {
  'needs-requirements': 'Needs requirements',
  'awaiting-req-approval': 'Needs you: approve requirements',
  'awaiting-plan-approval': 'Needs you: approve plan',
  'needs-dev': 'Needs dev',
  'needs-review': 'Needs review',
  'needs-qa': 'Needs QA',
  'awaiting-merge': 'Needs you: merge',
}
export const FLAG_TEXT: Record<FlagLabel, string> = { blocked: 'Blocked', 'changes-requested': 'Changes requested' }

/** Gate labels set by the Orchestrator; names are fixed by the server (service/workflow.go). */
export const GATE_LABEL: Record<Gate, StageLabel> = {
  requirements: 'awaiting-req-approval',
  plan: 'awaiting-plan-approval',
}

export interface Classified<L extends { name: string }> {
  kind: KindLabel | null
  stage: StageLabel | null
  flags: FlagLabel[]
  /** Labels that are not part of the workflow; rendered as plain chips. */
  other: L[]
  /** More than one stage label is present. */
  conflict: boolean
  /** Any workflow label at all. */
  hasWorkflow: boolean
}

const isStage = (n: string): n is StageLabel => (STAGE_LABELS as readonly string[]).includes(n)
const isFlag = (n: string): n is FlagLabel => (FLAG_LABELS as readonly string[]).includes(n)
const isKind = (n: string): n is KindLabel => (KIND_LABELS as readonly string[]).includes(n)

export function classifyLabels<L extends { name: string }>(labels: readonly L[]): Classified<L> {
  const stages = new Set<StageLabel>()
  const flags = new Set<FlagLabel>()
  const kinds = new Set<KindLabel>()
  const other: L[] = []
  for (const l of labels) {
    if (isStage(l.name)) stages.add(l.name)
    else if (isFlag(l.name)) flags.add(l.name)
    else if (isKind(l.name)) kinds.add(l.name)
    else other.push(l)
  }
  const stage = STAGE_LABELS.find((s) => stages.has(s)) ?? null
  const kind = KIND_LABELS.find((k) => kinds.has(k)) ?? null
  return {
    kind,
    stage,
    flags: FLAG_LABELS.filter((f) => flags.has(f)),
    other,
    conflict: stages.size > 1,
    hasWorkflow: stage !== null || flags.size > 0 || kind !== null,
  }
}

export const stageText = (s: StageLabel): string => STAGE_TEXT[s]

/** The in-app gate a stage label opens; the merge gate happens on GitHub and has none. */
export function gateOf(stage: string | null | undefined): Gate | null {
  if (stage === GATE_LABEL.requirements) return 'requirements'
  if (stage === GATE_LABEL.plan) return 'plan'
  return null
}

/** True for tickets waiting on a person: any `awaiting-*` stage or `blocked`. */
export function needsAttention(labels: readonly { name: string }[]): boolean {
  return labels.some((l) => (l.name.startsWith('awaiting-') && isStage(l.name)) || l.name === 'blocked')
}

/** An https link that is safe to render, with a short text ("#123" for pull URLs). */
export function prLink(url: string | null | undefined): { href: string; text: string } | null {
  if (!url) return null
  let u: URL
  try {
    u = new URL(url)
  } catch {
    return null
  }
  if (u.protocol !== 'https:' || !u.hostname) return null
  const m = /\/pull\/(\d+)\/?$/.exec(u.pathname)
  return { href: u.href, text: m ? `#${m[1]}` : 'PR' }
}

export type RoundTone = 'normal' | 'amber' | 'red'
export function roundTone(r: Round): RoundTone {
  if (r.n >= r.max) return 'red'
  if (r.n === r.max - 1) return 'amber'
  return 'normal'
}

/** The round only matters while the ticket is being reworked. */
export function showRound(c: Pick<Classified<never>, 'stage' | 'flags'>, r: Round | null | undefined): boolean {
  return !!r && (c.stage === 'needs-dev' || c.flags.includes('changes-requested'))
}
