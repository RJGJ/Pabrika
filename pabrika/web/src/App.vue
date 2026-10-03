<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'
import UnreachableState from '@/components/common/UnreachableState.vue'
import AppShell from '@/components/layout/AppShell.vue'
import { Toaster } from '@/components/ui/sonner'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const isPublic = computed(() => !!route.meta.public)
</script>

<template>
  <UnreachableState v-if="auth.status === 'unreachable'" />
  <div v-else-if="auth.status === 'unknown'" class="p-6 text-sm text-muted-foreground" role="status">Loading...</div>
  <RouterView v-else-if="isPublic || auth.status !== 'authed'" />
  <AppShell v-else />
  <Toaster close-button />
</template>
