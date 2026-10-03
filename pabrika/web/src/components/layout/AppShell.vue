<script setup lang="ts">
import { RouterView } from 'vue-router'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
</script>

<template>
  <SidebarProvider>
    <AppSidebar />
    <SidebarInset class="min-w-0">
      <header class="flex h-12 items-center gap-2 border-b px-3 md:hidden">
        <SidebarTrigger aria-label="Toggle sidebar" />
        <span class="font-semibold">Pabrika</span>
      </header>
      <main class="min-h-0 flex-1">
        <RouterView />
      </main>
    </SidebarInset>
  </SidebarProvider>
</template>
