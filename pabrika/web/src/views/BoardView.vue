<script setup lang="ts">
import { computed, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { SettingsIcon } from '@lucide/vue'
import { STATUSES, type Status } from '@/api/types'
import BoardColumn from '@/components/board/BoardColumn.vue'
import BoardSkeleton from '@/components/board/BoardSkeleton.vue'
import BoardToolbar from '@/components/board/BoardToolbar.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import LostAccessDialog from '@/components/common/LostAccessDialog.vue'
import TicketPanel from '@/components/ticket/TicketPanel.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useProjectEvents } from '@/composables/useProjectEvents'
import { useBoardStore } from '@/stores/board'
import { useProjectsStore } from '@/stores/projects'

const COLUMN_TITLES: Record<Status, string> = {
  backlog: 'Backlog',
  todo: 'To do',
  in_progress: 'In progress',
  done: 'Done',
}

const route = useRoute()
const router = useRouter()
const board = useBoardStore()
const projects = useProjectsStore()

const routeKey = computed(() => String(route.params.key ?? ''))
const panelOpen = computed(() => route.name === 'ticket')

function isOpenProject(key: string): boolean {
  const p = board.project
  return !!p && (key.toLowerCase() === p.key.toLowerCase() || key === p.id)
}

// Initial load. Coming back to the same, fully loaded project (for example from its settings)
// reloads quietly instead of flashing the skeleton.
void board.load(routeKey.value, {
  quiet: isOpenProject(routeKey.value) && board.loadState === 'ready' && board.ticketsLoaded,
})

// Key changes load the other project. A change that only canonicalizes the key (see below) is not a reload.
watch(routeKey, (key) => {
  if (key && !isOpenProject(key)) void board.load(key)
})

// Key normalization: /p/web and /p/<ULID> become /p/<KEY>.
watch(
  () => [board.project?.key, board.loadState] as const,
  ([key, state]) => {
    if (!key || state !== 'ready' || routeKey.value === key) return
    if (route.name !== 'board' && route.name !== 'ticket') return
    void router.replace({ name: route.name, params: { ...route.params, key }, query: route.query, hash: route.hash })
  },
  { immediate: true },
)

watch(
  () => [panelOpen.value, route.params.number, board.project?.key] as const,
  ([open, number, key]) => {
    board.selectedRef = open && key && typeof number === 'string' && /^\d+$/.test(number) ? `${key}-${number}` : null
  },
  { immediate: true },
)

watch(
  () => board.project?.key,
  (key) => {
    if (key && board.loadState === 'ready') projects.setLastProjectKey(key)
  },
)

useProjectEvents(() => routeKey.value)

function retry(): void {
  void board.load(routeKey.value)
}
</script>

<template>
  <div class="flex h-[calc(100svh-3rem)] flex-col md:h-svh">
    <BoardSkeleton v-if="board.loadState === 'idle' || board.loadState === 'loading'" />

    <EmptyState v-else-if="board.loadState === 'error'" title="Couldn't load the board" :description="board.error ?? undefined">
      <Button @click="retry">Retry</Button>
    </EmptyState>

    <EmptyState
      v-else-if="board.loadState === 'not-found'"
      title="Project not found"
      description="It doesn't exist, or you don't have access to it."
    >
      <Button as-child><RouterLink to="/">Go home</RouterLink></Button>
    </EmptyState>

    <EmptyState v-else-if="board.loadState === 'no-access'" title="No access to this project" />

    <template v-else-if="board.project">
      <header class="flex flex-wrap items-center gap-x-4 gap-y-2 border-b px-4 py-3">
        <div class="flex items-center gap-2">
          <h1 ref="heading" tabindex="-1" class="text-lg font-semibold outline-none">{{ board.project.name }}</h1>
          <span class="font-mono text-xs text-muted-foreground">{{ board.project.key }}</span>
          <Badge variant="outline" class="capitalize">{{ board.project.role }}</Badge>
          <Badge v-if="board.isArchived" variant="secondary">Archived</Badge>
          <Badge v-else-if="!board.canEdit" variant="secondary">View only</Badge>
        </div>
        <div class="flex min-w-0 flex-1 items-center gap-2">
          <BoardToolbar class="flex-1" />
          <Button as-child variant="ghost" size="icon" aria-label="Project settings">
            <RouterLink :to="{ name: 'project-settings', params: { key: board.project.key } }"><SettingsIcon /></RouterLink>
          </Button>
        </div>
      </header>

      <div
        v-if="board.isArchived"
        role="status"
        class="flex items-center gap-3 border-b bg-amber-50 px-4 py-2 text-sm text-amber-900 dark:bg-amber-950/40 dark:text-amber-200"
      >
        This project is archived and read-only.
        <RouterLink
          v-if="board.isOwner"
          :to="{ name: 'project-settings', params: { key: board.project.key } }"
          class="font-medium underline"
        >Unarchive</RouterLink>
      </div>

      <div class="flex min-h-0 flex-1 gap-3 overflow-x-auto p-4">
        <BoardColumn v-for="s in STATUSES" :key="s" :status="s" :title="COLUMN_TITLES[s]" />
      </div>

      <TicketPanel v-if="panelOpen" />
    </template>

    <LostAccessDialog />
  </div>
</template>
