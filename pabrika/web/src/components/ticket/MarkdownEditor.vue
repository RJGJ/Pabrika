<script setup lang="ts">
import { computed, ref } from 'vue'
import MarkdownView from '@/components/ticket/MarkdownView.vue'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'

const props = withDefaults(
  defineProps<{
    modelValue: string
    max?: number
    saving?: boolean
    error?: string | null
    submitLabel?: string
    placeholder?: string
    label?: string
    showCancel?: boolean
    /** Allow an empty body (a description may be cleared; a comment may not). */
    allowEmpty?: boolean
  }>(),
  { max: 20000, saving: false, submitLabel: 'Save', showCancel: true, allowEmpty: false, label: 'Markdown editor' },
)
const emit = defineEmits<{ 'update:modelValue': [value: string]; submit: []; cancel: [] }>()

const tab = ref('write')
const length = computed(() => props.modelValue.length)
const tooLong = computed(() => length.value > props.max)
const canSubmit = computed(() => !props.saving && !tooLong.value && (props.allowEmpty || props.modelValue.trim().length > 0))

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Enter' && (e.ctrlKey || e.metaKey)) {
    e.preventDefault()
    if (canSubmit.value) emit('submit')
  }
}
</script>

<template>
  <div class="grid gap-2">
    <Tabs v-model="tab">
      <TabsList>
        <TabsTrigger value="write">Write</TabsTrigger>
        <TabsTrigger value="preview">Preview</TabsTrigger>
      </TabsList>
      <TabsContent value="write">
        <Textarea
          :model-value="modelValue"
          :placeholder="placeholder"
          :aria-label="label"
          :aria-invalid="tooLong || !!error ? true : undefined"
          rows="5"
          @update:model-value="emit('update:modelValue', String($event))"
          @keydown="onKeydown"
        />
      </TabsContent>
      <TabsContent value="preview">
        <div class="min-h-24 rounded-md border p-3">
          <MarkdownView v-if="modelValue.trim()" :source="modelValue" />
          <p v-else class="text-sm text-muted-foreground">Nothing to preview</p>
        </div>
      </TabsContent>
    </Tabs>
    <p v-if="error" role="alert" class="text-xs text-destructive">{{ error }}</p>
    <div class="flex items-center gap-2">
      <span :class="['text-xs', tooLong ? 'text-destructive' : 'text-muted-foreground']">{{ length }} / {{ max }}</span>
      <div class="ml-auto flex gap-2">
        <Button v-if="showCancel" variant="ghost" size="sm" :disabled="saving" @click="emit('cancel')">Cancel</Button>
        <Button size="sm" :disabled="!canSubmit" @click="emit('submit')">{{ submitLabel }}</Button>
      </div>
    </div>
  </div>
</template>
