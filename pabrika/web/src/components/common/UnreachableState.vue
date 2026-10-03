<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { Button } from '@/components/ui/button'
import EmptyState from '@/components/common/EmptyState.vue'
import { guardDecision } from '@/router'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const router = useRouter()
const retrying = ref(false)

async function retry() {
  retrying.value = true
  try {
    await auth.bootstrap()
    const decision = guardDecision(router.currentRoute.value, auth)
    if (decision !== true) await router.replace(decision)
  } finally {
    retrying.value = false
  }
}
</script>

<template>
  <div class="flex min-h-screen items-center justify-center">
    <EmptyState title="Can't reach the server" description="Check your connection and try again.">
      <Button :disabled="retrying" @click="retry">Retry</Button>
    </EmptyState>
  </div>
</template>
