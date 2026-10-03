<script setup lang="ts">
import { Skeleton } from '@/components/ui/skeleton'
</script>

<template>
  <div class="flex h-full gap-3 overflow-x-auto p-4" role="status" aria-label="Loading board">
    <div v-for="c in 4" :key="c" class="flex min-w-64 flex-1 flex-col gap-2 rounded-lg border bg-muted/40 p-3">
      <Skeleton class="h-4 w-24" />
      <Skeleton v-for="i in 3" :key="i" class="h-20 w-full" />
    </div>
  </div>
</template>
