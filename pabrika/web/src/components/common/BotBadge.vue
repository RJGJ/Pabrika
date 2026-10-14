<script setup lang="ts">
import { Badge } from '@/components/ui/badge'

/** The "bot" badge. With `role` (a parsed `[role]` tag, bot authors only) a second chip follows it. */
defineProps<{ role?: string }>()
</script>

<template>
  <Badge v-if="!role" variant="secondary" class="px-1.5 py-0 text-[10px] uppercase">bot</Badge>
  <span v-else class="inline-flex items-center gap-1">
    <Badge variant="secondary" class="px-1.5 py-0 text-[10px] uppercase">bot</Badge>
    <Badge variant="outline" class="px-1.5 py-0 text-[10px]" data-testid="role-chip">{{ role }}</Badge>
  </span>
</template>
