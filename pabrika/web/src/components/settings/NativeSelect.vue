<script setup lang="ts">
import { cn } from '@/lib/utils'

// A styled native <select>: accessible, keyboard friendly and trivial to drive in tests.
defineProps<{
  id?: string
  options: { value: string; label: string }[]
  disabled?: boolean
  ariaLabel?: string
  class?: string
}>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <select
    :id="id"
    v-model="model"
    :disabled="disabled"
    :aria-label="ariaLabel"
    :class="
      cn(
        'border-input bg-background h-9 rounded-md border px-3 text-sm shadow-xs outline-none focus-visible:border-ring focus-visible:ring-3 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50',
        $props.class,
      )
    "
  >
    <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
  </select>
</template>
