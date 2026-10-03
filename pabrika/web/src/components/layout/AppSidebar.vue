<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { PlusIcon, SettingsIcon } from '@lucide/vue'
import CreateProjectDialog from '@/components/project/CreateProjectDialog.vue'
import UserMenu from '@/components/layout/UserMenu.vue'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import {
  Sidebar, SidebarContent, SidebarFooter, SidebarGroup, SidebarGroupContent, SidebarGroupLabel, SidebarHeader,
  SidebarMenu, SidebarMenuButton, SidebarMenuItem, SidebarMenuSkeleton,
} from '@/components/ui/sidebar'
import { Switch } from '@/components/ui/switch'
import { useProjectsStore } from '@/stores/projects'

const projects = useProjectsStore()
const route = useRoute()
const createOpen = ref(false)

onMounted(() => {
  if (!projects.loaded && !projects.loading) void projects.fetch()
})

function isActive(key: string): boolean {
  const k = route.params.key
  return typeof k === 'string' && k.toLowerCase() === key.toLowerCase()
}
</script>

<template>
  <Sidebar>
    <SidebarHeader>
      <RouterLink to="/" class="px-2 py-1 text-lg font-semibold tracking-tight">Pabrika</RouterLink>
    </SidebarHeader>
    <SidebarContent>
      <SidebarGroup>
        <SidebarGroupLabel>Projects</SidebarGroupLabel>
        <SidebarGroupContent>
          <SidebarMenu v-if="projects.loading && !projects.loaded">
            <SidebarMenuItem v-for="i in 3" :key="i"><SidebarMenuSkeleton /></SidebarMenuItem>
          </SidebarMenu>
          <div v-else-if="projects.error && !projects.loaded" class="px-2 text-sm" role="alert">
            <p class="text-destructive">Couldn't load projects.</p>
            <Button variant="link" size="sm" class="h-auto px-0" @click="projects.fetch()">Retry</Button>
          </div>
          <SidebarMenu v-else>
            <SidebarMenuItem v-for="p in projects.sidebarList" :key="p.id">
              <SidebarMenuButton as-child :is-active="isActive(p.key)">
                <RouterLink :to="{ name: 'board', params: { key: p.key } }">
                  <span class="truncate">{{ p.name }}</span>
                  <span class="ml-auto font-mono text-xs text-muted-foreground">{{ p.key }}</span>
                </RouterLink>
              </SidebarMenuButton>
            </SidebarMenuItem>
            <SidebarMenuItem v-if="projects.sidebarList.length === 0">
              <p class="px-2 py-1 text-sm text-muted-foreground">No projects yet</p>
            </SidebarMenuItem>
            <SidebarMenuItem>
              <SidebarMenuButton @click="createOpen = true"><PlusIcon /><span>New project</span></SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
          <div class="mt-2 flex items-center gap-2 px-2">
            <Switch
              id="show-archived"
              :model-value="projects.includeArchived"
              @update:model-value="projects.setIncludeArchived($event)"
            />
            <Label for="show-archived" class="text-xs text-muted-foreground">Show archived</Label>
          </div>
        </SidebarGroupContent>
      </SidebarGroup>
    </SidebarContent>
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton as-child :is-active="route.name === 'account'">
            <RouterLink :to="{ name: 'account' }"><SettingsIcon /><span>Account settings</span></RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
      <UserMenu />
    </SidebarFooter>
    <CreateProjectDialog v-model:open="createOpen" />
  </Sidebar>
</template>
