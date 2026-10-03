<script setup lang="ts">
import { onBeforeUnmount, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import EmptyState from '@/components/common/EmptyState.vue'
import GeneralSettings from '@/components/settings/GeneralSettings.vue'
import LabelsManager from '@/components/settings/LabelsManager.vue'
import MembersTable from '@/components/settings/MembersTable.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import LostAccessDialog from '@/components/common/LostAccessDialog.vue'
import { useProjectEvents } from '@/composables/useProjectEvents'
import { useProjectSettingsStore } from '@/stores/projectSettings'

const route = useRoute()
const store = useProjectSettingsStore()

watch(
  () => route.params.key,
  (key) => {
    if (typeof key === 'string' && key) void store.load(key)
  },
  { immediate: true },
)

// Live updates share the board's stream logic; leave and delete close the stream first.
const events = useProjectEvents(
  () => (typeof route.params.key === 'string' ? route.params.key : null),
  {
    ready: () => store.loadState === 'ready',
    lost: () => store.loadState === 'no-access',
    setLive: () => {},
    onEvent: (e) => void store.applyEvent(e),
    onOpen: () => void store.refresh(),
    onLostAccess: () => store.handleLostAccess(),
  },
)
store.registerStream(events)

onBeforeUnmount(() => {
  store.registerStream(null)
  store.reset()
})

function retry() {
  const key = route.params.key
  if (typeof key === 'string') void store.load(key)
}
</script>

<template>
  <div class="mx-auto max-w-4xl p-4 md:p-6">
    <div v-if="store.loadState === 'loading' || store.loadState === 'idle'" class="grid gap-4" data-testid="settings-loading">
      <Skeleton class="h-8 w-64" />
      <Skeleton class="h-10 w-80" />
      <Skeleton class="h-48 w-full" />
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

    <EmptyState v-else-if="store.loadState === 'error'" title="Could not load project settings">
      <Button variant="outline" @click="retry">Retry</Button>
    </EmptyState>

    <template v-else-if="store.project">
      <header class="mb-6 flex flex-wrap items-center gap-2">
        <h1 class="text-xl font-semibold">{{ store.project.name }}</h1>
        <Badge variant="outline" class="font-mono">{{ store.project.key }}</Badge>
        <Badge variant="secondary" class="capitalize" data-testid="role-badge">{{ store.project.role }}</Badge>
        <Badge v-if="store.isArchived" variant="secondary">Archived</Badge>
        <Button as-child variant="link" class="ml-auto">
          <RouterLink :to="{ name: 'board', params: { key: store.project.key } }">Back to board</RouterLink>
        </Button>
      </header>

      <Tabs default-value="general">
        <TabsList>
          <TabsTrigger value="general">General</TabsTrigger>
          <TabsTrigger value="members">Members</TabsTrigger>
          <TabsTrigger value="labels">Labels</TabsTrigger>
        </TabsList>
        <TabsContent value="general" class="pt-4"><GeneralSettings /></TabsContent>
        <TabsContent value="members" class="pt-4"><MembersTable /></TabsContent>
        <TabsContent value="labels" class="pt-4"><LabelsManager /></TabsContent>
      </Tabs>
    </template>
    <LostAccessDialog />
  </div>
</template>
