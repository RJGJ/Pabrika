<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import EmptyState from '@/components/common/EmptyState.vue'
import CreateProjectDialog from '@/components/project/CreateProjectDialog.vue'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { pickHomeProject } from '@/lib/home'
import { useProjectsStore } from '@/stores/projects'

const projects = useProjectsStore()
const router = useRouter()
const createOpen = ref(false)

function go() {
  if (!projects.loaded) return
  const key = pickHomeProject(projects.list, projects.lastProjectKey)
  if (key) void router.replace({ name: 'board', params: { key } })
}

onMounted(async () => {
  await projects.fetch()
  go()
})
watch(() => projects.loaded, go)
</script>

<template>
  <div class="p-6">
    <div v-if="projects.loading && !projects.loaded" class="grid max-w-md gap-3" aria-busy="true">
      <Skeleton class="h-6 w-48" />
      <Skeleton class="h-4 w-72" />
    </div>
    <EmptyState v-else-if="projects.error && !projects.loaded" title="Couldn't load your projects" :description="projects.error">
      <Button @click="projects.fetch().then(go)">Retry</Button>
    </EmptyState>
    <EmptyState
      v-else-if="projects.loaded && projects.list.length === 0"
      title="No projects yet"
      description="Create a project to start tracking tickets."
    >
      <Button @click="createOpen = true">Create your first project</Button>
    </EmptyState>
    <CreateProjectDialog v-model:open="createOpen" />
  </div>
</template>
