<script setup lang="ts">
import { computed } from 'vue'
import { cn } from '@/lib/utils'
import { useBoardStore } from '@/stores/board'

const board = useBoardStore()
const label = computed(() => (board.live === 'live' ? 'Live' : board.live === 'reconnecting' ? 'Reconnecting' : ''))
</script>

<template>
  <div aria-live="polite" role="status" class="inline-flex min-w-24 items-center gap-1.5 text-xs text-muted-foreground">
    <template v-if="label">
      <span
        aria-hidden="true"
        :class="
          cn(
            'size-2 rounded-full',
            board.live === 'live' ? 'bg-green-500' : 'bg-amber-500 motion-safe:animate-pulse',
          )
        "
      />
      <span>{{ label }}</span>
    </template>
  </div>
</template>
