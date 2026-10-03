<script setup lang="ts">
import { Label } from '@/components/ui/label'

defineProps<{ id: string; label: string; error?: string | null; hint?: string }>()
</script>

<template>
  <div class="grid gap-1.5">
    <Label :for="id">{{ label }}</Label>
    <slot />
    <p v-if="hint && !error" class="text-xs text-muted-foreground">{{ hint }}</p>
    <p v-if="error" :id="`${id}-error`" role="alert" class="text-xs text-destructive">{{ error }}</p>
  </div>
</template>
