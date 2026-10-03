<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { PlusIcon } from '@lucide/vue'
import { ApiError } from '@/api/client'
import type { Status } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useBoardStore } from '@/stores/board'

const props = defineProps<{ status: Status }>()
const board = useBoardStore()

const open = ref(false)
const text = ref('')
const fieldError = ref<string | null>(null)
const input = ref<InstanceType<typeof Input> | null>(null)

async function openField(): Promise<void> {
  open.value = true
  await nextTick()
  focusInput()
}

function focusInput(): void {
  ;(input.value?.$el as HTMLInputElement | undefined)?.focus()
}

function close(): void {
  open.value = false
  text.value = ''
  fieldError.value = null
}

async function submit(): Promise<void> {
  const title = String(text.value).trim()
  if (!title) return
  // Clear right away so entries can follow each other; restore on failure.
  text.value = ''
  fieldError.value = null
  try {
    await board.createTicket(props.status, title)
  } catch (e) {
    if (!String(text.value).trim()) text.value = title
    if (e instanceof ApiError && e.status === 422) fieldError.value = e.fields?.title ?? e.message
  }
  await nextTick()
  focusInput()
}

function onBlur(): void {
  if (!String(text.value).trim()) close()
}
</script>

<template>
  <div>
    <Button v-if="!open" variant="ghost" size="sm" class="w-full justify-start text-muted-foreground" @click="openField">
      <PlusIcon /> Add ticket
    </Button>
    <div v-else class="grid gap-1">
      <Input
        ref="input"
        v-model="text"
        :maxlength="200"
        placeholder="Ticket title"
        aria-label="New ticket title"
        :aria-invalid="fieldError ? true : undefined"
        @keydown.enter.prevent="submit"
        @keydown.esc.prevent="close"
        @blur="onBlur"
      />
      <p v-if="fieldError" role="alert" class="text-xs text-destructive">{{ fieldError }}</p>
    </div>
  </div>
</template>
