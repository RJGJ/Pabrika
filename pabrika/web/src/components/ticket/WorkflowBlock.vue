<script setup lang="ts">
import { computed } from 'vue'
import { CopyIcon, GitPullRequestIcon } from '@lucide/vue'
import type { Decision, Workflow, WorkflowDecision } from '@/api/types'
import DecisionCard from '@/components/ticket/DecisionCard.vue'
import GateActions from '@/components/ticket/GateActions.vue'
import WorkflowBadges from '@/components/ticket/WorkflowBadges.vue'
import { Button } from '@/components/ui/button'
import { notify } from '@/lib/toast'
import { classifyLabels, gateOf, prLink } from '@/lib/workflow'

const props = defineProps<{
  ticketId: string
  ticketRef: string
  labels: readonly { name: string }[]
  workflow?: Workflow | null
  /** Latest decision per gate (ticket detail only). */
  decisions?: WorkflowDecision[]
}>()
const emit = defineEmits<{ recorded: [decision: Decision]; stale: [] }>()

const classified = computed(() => classifyLabels(props.labels))
const pr = computed(() => prLink(props.workflow?.pr_url))
const branch = computed(() => props.workflow?.branch ?? null)
const round = computed(() => props.workflow?.round ?? null)
const gate = computed(() => gateOf(classified.value.stage))
const mergeGate = computed(() => classified.value.stage === 'awaiting-merge')
const shown = computed(
  () => classified.value.hasWorkflow || !!pr.value || !!branch.value || !!round.value || (props.decisions?.length ?? 0) > 0,
)

async function copyBranch(): Promise<void> {
  if (!branch.value) return
  try {
    await navigator.clipboard.writeText(branch.value)
    notify('success', 'Branch name copied')
  } catch {
    notify('error', "Couldn't copy the branch name")
  }
}
</script>

<template>
  <section v-if="shown" class="grid gap-3 rounded-md border p-3" aria-labelledby="ticket-workflow-h" data-testid="workflow-block">
    <h3 id="ticket-workflow-h" class="text-sm font-semibold">Workflow</h3>

    <div class="flex flex-wrap items-center gap-1.5">
      <WorkflowBadges :classified="classified" :round="round" />
      <span v-if="classified.kind" class="rounded border px-1 text-[10px] uppercase">{{ classified.kind }}</span>
      <span v-if="!classified.stage && !classified.flags.length" class="text-sm text-muted-foreground">No stage</span>
    </div>
    <p v-if="classified.conflict" role="note" class="text-xs text-amber-700 dark:text-amber-300" data-testid="conflict-hint">
      Conflicting stage labels: the Orchestrator should leave only one needs-* or awaiting-* label.
    </p>
    <p v-if="round" class="text-sm" data-testid="round-text">Round {{ round.n }}/{{ round.max }}</p>

    <div v-if="pr || branch" class="grid gap-1.5 text-sm">
      <a
        v-if="pr"
        :href="pr.href"
        target="_blank"
        rel="noopener noreferrer"
        data-testid="panel-pr"
        class="inline-flex w-fit items-center gap-1 underline underline-offset-2"
      >
        <GitPullRequestIcon class="size-4" aria-hidden="true" /> Pull request {{ pr.text }}
      </a>
      <div v-if="branch" class="flex items-center gap-2">
        <span class="text-xs text-muted-foreground">Branch</span>
        <code class="rounded bg-muted px-1.5 py-0.5 font-mono text-xs break-all" data-testid="panel-branch">{{ branch }}</code>
        <Button variant="ghost" size="xs" aria-label="Copy branch name" @click="copyBranch"><CopyIcon /></Button>
      </div>
    </div>

    <div v-if="decisions?.length" class="grid gap-2" aria-label="Decisions">
      <DecisionCard v-for="d in decisions" :key="d.gate" :decision="d" :waiting="gate === d.gate" />
    </div>

    <template v-if="gate">
      <GateActions
        :ticket-id="ticketId"
        :ticket-ref="ticketRef"
        :labels="labels"
        @recorded="emit('recorded', $event)"
        @stale="emit('stale')"
      />
      <p class="text-xs text-muted-foreground">The Orchestrator applies the next stage after a decision.</p>
    </template>
    <p v-if="mergeGate" class="text-xs text-muted-foreground" data-testid="merge-note">
      Merging the pull request is done on GitHub, not here.
    </p>
  </section>
</template>
