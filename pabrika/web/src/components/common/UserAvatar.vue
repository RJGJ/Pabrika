<script setup lang="ts">
import { computed } from 'vue'
import { Avatar, AvatarFallback } from '@/components/ui/avatar'
import { cn } from '@/lib/utils'

const props = defineProps<{ name: string; class?: string }>()

const initials = computed(() => {
  const parts = props.name.trim().split(/\s+/).filter(Boolean)
  if (parts.length === 0) return '?'
  const first = parts[0][0] ?? ''
  const last = parts.length > 1 ? (parts[parts.length - 1][0] ?? '') : ''
  return (first + last).toUpperCase()
})
</script>

<template>
  <Avatar :class="cn('size-6 text-[10px]', props.class)" :title="name">
    <AvatarFallback>{{ initials }}</AvatarFallback>
  </Avatar>
</template>
