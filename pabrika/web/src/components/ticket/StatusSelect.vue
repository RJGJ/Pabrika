<script setup lang="ts">
import { STATUSES, type Status } from '@/api/types'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

defineProps<{ modelValue: Status; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: Status] }>()
const LABEL: Record<Status, string> = { backlog: 'Backlog', todo: 'To do', in_progress: 'In progress', done: 'Done' }
</script>

<template>
  <!-- The keyboard alternative to dragging: picking a status moves the ticket to the bottom of that column. -->
  <Select :model-value="modelValue" :disabled="disabled" @update:model-value="emit('update:modelValue', $event as Status)">
    <SelectTrigger class="w-full" aria-label="Status"><SelectValue /></SelectTrigger>
    <SelectContent>
      <SelectItem v-for="s in STATUSES" :key="s" :value="s">{{ LABEL[s] }}</SelectItem>
    </SelectContent>
  </Select>
</template>
