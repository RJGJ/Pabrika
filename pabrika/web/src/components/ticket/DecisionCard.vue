<script setup lang="ts">
import { computed } from 'vue'
import { CheckCircle2Icon, MessageSquareWarningIcon } from '@lucide/vue'
import type { WorkflowDecision } from '@/api/types'
import MarkdownView from '@/components/ticket/MarkdownView.vue'
import { toneClasses } from '@/lib/colors'
import { relativeTime } from '@/lib/dates'
import { cn } from '@/lib/utils'

const props = defineProps<{
  decision: WorkflowDecision
  /** The ticket still carries this gate's label, so the Orchestrator has not acted yet. */
  waiting?: boolean
}>()

const GATE: Record<string, string> = { requirements: 'requirements', plan: 'plan' }
const approved = computed(() => props.decision.verdict === 'approved')
const stale = computed(() => props.decision.current === false)
</script>

<template>
  <div
    data-testid="decision-card"
    :data-verdict="decision.verdict"
    :class="cn('grid gap-1 rounded-md border p-2.5 text-sm', stale && 'opacity-70')"
  >
    <div class="flex flex-wrap items-center gap-2">
      <span :class="cn('inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium', toneClasses(approved ? 'green' : 'orange'))">
        <CheckCircle2Icon v-if="approved" class="size-3" aria-hidden="true" />
        <MessageSquareWarningIcon v-else class="size-3" aria-hidden="true" />
        {{ approved ? 'Approved' : 'Changes requested' }}
      </span>
      <span class="text-xs text-muted-foreground capitalize">{{ GATE[decision.gate] ?? decision.gate }}</span>
      <span class="text-sm">
        by <span class="font-medium">{{ decision.by.name }}</span>,
        <time :datetime="decision.at" :title="decision.at">{{ relativeTime(decision.at) }}</time>
      </span>
      <span v-if="stale" class="text-xs text-muted-foreground">Earlier round</span>
    </div>
    <MarkdownView v-if="decision.note" :source="decision.note" />
    <p v-if="waiting && !stale && approved" class="text-xs text-muted-foreground">Approved, waiting for the Orchestrator.</p>
  </div>
</template>
