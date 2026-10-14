<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { GitPullRequestIcon, MessageSquareIcon } from '@lucide/vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import WorkflowBadges from '@/components/ticket/WorkflowBadges.vue'
import { formatDueDate, isOverdue } from '@/lib/dates'
import { labelClasses, priorityClasses } from '@/lib/colors'
import { cn } from '@/lib/utils'
import { classifyLabels, prLink, showRound } from '@/lib/workflow'
import { isTempId, useBoardStore } from '@/stores/board'

const props = defineProps<{ id: string }>()
const board = useBoardStore()
const route = useRoute()

const ticket = computed(() => board.ticketsById[props.id])
const temp = computed(() => isTempId(props.id))
const flashing = computed(() => board.flashIds.has(props.id))
const overdue = computed(() => !!ticket.value && isOverdue(ticket.value.due_date, ticket.value.status))
const to = computed(() =>
  ticket.value && board.project
    ? { name: 'ticket', params: { key: board.project.key, number: String(ticket.value.number) }, query: route.query }
    : undefined,
)
const classified = computed(() => classifyLabels(ticket.value?.labels ?? []))
const pr = computed(() => prLink(ticket.value?.workflow?.pr_url))
const round = computed(() => ticket.value?.workflow?.round ?? null)
const hasBadges = computed(
  () => !!classified.value.stage || classified.value.flags.length > 0 || showRound(classified.value, round.value) || !!pr.value,
)
const PRIORITY_LABEL: Record<string, string> = { low: 'Low', medium: 'Medium', high: 'High', urgent: 'Urgent' }
</script>

<template>
  <component
    :is="temp || !to ? 'div' : RouterLink"
    v-if="ticket"
    v-bind="temp || !to ? {} : { to }"
    :data-id="id"
    :data-draggable="temp ? undefined : ''"
    :data-ticket-ref="ticket.ref"
    :aria-busy="temp || undefined"
    :class="
      cn(
        'block rounded-md border bg-card p-2.5 text-sm text-card-foreground shadow-xs outline-none',
        'focus-visible:ring-2 focus-visible:ring-ring',
        temp ? 'opacity-60' : 'hover:border-ring/60',
        flashing && 'flash-card',
      )
    "
  >
    <div class="flex items-center justify-between gap-2 text-xs text-muted-foreground">
      <span class="flex items-center gap-1.5">
        <span class="font-mono">{{ temp ? 'Saving...' : ticket.ref }}</span>
        <span
          v-if="classified.kind"
          data-testid="kind-tag"
          class="rounded border px-1 text-[10px] uppercase"
        >{{ classified.kind }}</span>
      </span>
      <span :class="cn('rounded px-1.5 py-0.5 font-medium', priorityClasses(ticket.priority))">
        {{ PRIORITY_LABEL[ticket.priority] ?? ticket.priority }}
      </span>
    </div>
    <p class="mt-1 line-clamp-3 font-medium break-words">{{ ticket.title }}</p>
    <div v-if="hasBadges" class="mt-2 flex flex-wrap items-center gap-1">
      <WorkflowBadges :classified="classified" :round="round" />
      <a
        v-if="pr"
        :href="pr.href"
        target="_blank"
        rel="noopener noreferrer"
        data-testid="pr-chip"
        class="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-[11px] font-medium hover:bg-accent"
        @click.stop
      >
        <GitPullRequestIcon class="size-3" aria-hidden="true" />
        <span class="sr-only">Pull request </span>{{ pr.text }}
      </a>
    </div>
    <div v-if="classified.other.length" class="mt-2 flex flex-wrap gap-1">
      <span
        v-for="l in classified.other"
        :key="l.id"
        :class="cn('rounded-full px-2 py-0.5 text-[11px]', labelClasses(l.color))"
      >{{ l.name }}</span>
    </div>
    <div class="mt-2 flex items-center gap-2 text-xs text-muted-foreground">
      <span v-if="ticket.due_date" :class="overdue ? 'font-medium text-red-600 dark:text-red-400' : ''">
        <span v-if="overdue" class="sr-only">Overdue: </span>{{ formatDueDate(ticket.due_date) }}
      </span>
      <span v-if="ticket.comment_count > 0" class="inline-flex items-center gap-1">
        <MessageSquareIcon class="size-3.5" aria-hidden="true" />
        <span class="sr-only">Comments: </span>{{ ticket.comment_count }}
      </span>
      <UserAvatar v-if="ticket.assignee" :name="ticket.assignee.display_name" class="ml-auto" />
    </div>
  </component>
</template>
