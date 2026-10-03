<script setup lang="ts">
import { PRIORITIES, type Priority } from '@/api/types'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

defineProps<{ modelValue: Priority; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: Priority] }>()
const LABEL: Record<Priority, string> = { low: 'Low', medium: 'Medium', high: 'High', urgent: 'Urgent' }
</script>

<template>
  <Select :model-value="modelValue" :disabled="disabled" @update:model-value="emit('update:modelValue', $event as Priority)">
    <SelectTrigger class="w-full" aria-label="Priority"><SelectValue /></SelectTrigger>
    <SelectContent>
      <SelectItem v-for="p in PRIORITIES" :key="p" :value="p">{{ LABEL[p] }}</SelectItem>
    </SelectContent>
  </Select>
</template>
