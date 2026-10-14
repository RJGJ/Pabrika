<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { XIcon } from '@lucide/vue'
import ActivityFeedRow from '@/components/activity/ActivityFeedRow.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import NativeSelect from '@/components/settings/NativeSelect.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { useProjectEvents } from '@/composables/useProjectEvents'
import {
  emptyFeedFilters, FEED_FILTER_KEYS, feedFiltersActive, parseFeedFilters, serializeFeedFilters, type FeedFilters,
} from '@/lib/feedFilters'
import { ROLE_LABEL, TAGGED_ROLES } from '@/lib/roleTag'
import { useActivityFeedStore } from '@/stores/activityFeed'

const route = useRoute()
const router = useRouter()
const store = useActivityFeedStore()

const key = computed(() => String(route.params.key ?? ''))

// Filters live in the URL query (`?actor&role&ticket&gates`), like the board filters.
const filters = computed<FeedFilters>(() => parseFeedFilters(route.query as Record<string, string | null | (string | null)[]>))
watch(filters, (f) => void store.setFilters(f), { immediate: true })

watch(
  key,
  (k) => {
    if (k) void store.load(k)
  },
  { immediate: true },
)

function write(f: FeedFilters): void {
  const query: Record<string, unknown> = { ...route.query }
  for (const k of FEED_FILTER_KEYS) delete query[k]
  Object.assign(query, serializeFeedFilters(f))
  void router.replace({ name: route.name ?? undefined, params: route.params, query } as never)
}
const update = (patch: Partial<FeedFilters>) => write({ ...filters.value, ...patch })

const active = computed(() => feedFiltersActive(filters.value))
const ACTOR_OPTIONS = [
  { value: 'all', label: 'Everyone' },
  { value: 'agent', label: 'Agents' },
  { value: 'human', label: 'People' },
]
const ROLE_OPTIONS = [{ value: 'any', label: 'Any role' }, ...TAGGED_ROLES.map((r) => ({ value: r, label: ROLE_LABEL[r] }))]

// Live: a "N new events" bar instead of shifting the list under the reader.
useProjectEvents(() => (store.key ? key.value : null), {
  ready: () => store.loadState === 'ready',
  lost: () => store.loadState === 'no-access',
  setLive: (s) => {
    store.live = s
  },
  onEvent: (e) => store.noteEvent(e),
  onOpen: () => void store.onStreamOpen(),
  onLostAccess: () => store.handleLostAccess(),
})

onBeforeUnmount(() => {
  store.live = 'idle'
})

const newLabel = computed(() => `${store.pending} new event${store.pending === 1 ? '' : 's'}, show`)
</script>

<template>
  <div class="mx-auto max-w-4xl p-4 md:p-6">
    <header class="mb-4 flex flex-wrap items-center gap-2">
      <h1 class="text-xl font-semibold">Activity</h1>
      <span class="font-mono text-sm text-muted-foreground">{{ key }}</span>
      <Button as-child variant="link" class="ml-auto">
        <RouterLink :to="{ name: 'board', params: { key } }">Back to board</RouterLink>
      </Button>
    </header>

    <div v-if="store.loadState === 'loading' || store.loadState === 'idle'" class="grid gap-3" data-testid="feed-loading">
      <Skeleton class="h-9 w-full" />
      <Skeleton class="h-14 w-full" />
      <Skeleton class="h-14 w-full" />
    </div>

    <EmptyState v-else-if="store.loadState === 'not-found'" title="Project not found">
      <Button as-child variant="outline"><RouterLink to="/">Go home</RouterLink></Button>
    </EmptyState>

    <EmptyState
      v-else-if="store.loadState === 'no-access'"
      title="You no longer have access to this project"
      description="You were removed from it, or it was deleted."
    >
      <Button as-child variant="outline"><RouterLink to="/">Go home</RouterLink></Button>
    </EmptyState>

    <EmptyState v-else-if="store.loadState === 'error'" title="Could not load the activity">
      <Button variant="outline" @click="store.load(key)">Retry</Button>
    </EmptyState>

    <template v-else>
      <div class="mb-3 flex flex-wrap items-center gap-2" role="search" aria-label="Filter activity">
        <NativeSelect
          :model-value="filters.actor"
          :options="ACTOR_OPTIONS"
          aria-label="Filter by actor"
          @update:model-value="update({ actor: $event as FeedFilters['actor'] })"
        />
        <NativeSelect
          :model-value="filters.role ?? 'any'"
          :options="ROLE_OPTIONS"
          aria-label="Filter by role"
          @update:model-value="update({ role: $event === 'any' ? null : ($event as FeedFilters['role']) })"
        />
        <Input
          :model-value="filters.ticket"
          placeholder="Ticket, e.g. WEB-30"
          aria-label="Filter by ticket"
          class="h-9 w-44"
          @update:model-value="update({ ticket: String($event).trim() })"
        />
        <Button
          variant="outline"
          size="sm"
          data-testid="gates-only"
          :aria-pressed="filters.gates"
          :class="filters.gates ? 'border-ring bg-accent' : ''"
          @click="update({ gates: !filters.gates })"
        >Gates only</Button>
        <Button v-if="active" variant="ghost" size="sm" @click="write(emptyFeedFilters())"><XIcon /> Clear filters</Button>
      </div>

      <!-- Reserved height: showing or hiding the bar never moves the list. -->
      <div class="mb-2 h-9" aria-live="polite">
        <Button v-if="store.pending > 0" variant="secondary" size="sm" class="w-full" data-testid="new-events" @click="store.showNew()">
          {{ newLabel }}
        </Button>
      </div>

      <EmptyState
        v-if="store.items.length === 0 && !active"
        title="Nothing has happened in this project yet"
      />
      <p v-else-if="store.visible.length === 0" class="py-6 text-center text-sm text-muted-foreground" data-testid="no-match">
        No events match these filters.
        <Button v-if="store.hasMore" variant="link" size="sm" :disabled="store.loadingMore" @click="store.loadMore()">Load more</Button>
      </p>
      <ul v-else aria-label="Project activity">
        <ActivityFeedRow v-for="i in store.visible" :key="i.id" :item="i" :project-key="key" :members="store.members" />
      </ul>
      <div v-if="store.visible.length > 0 && store.hasMore" class="mt-4 text-center">
        <Button variant="outline" size="sm" :disabled="store.loadingMore" @click="store.loadMore()">Load more</Button>
      </div>
    </template>
  </div>
</template>
