<script setup lang="ts">
import { ref } from 'vue'
import { ApiError } from '@/api/client'
import { comments as commentsApi } from '@/api/comments'
import type { Comment } from '@/api/types'
import MarkdownEditor from '@/components/ticket/MarkdownEditor.vue'
import { notify } from '@/lib/toast'

const props = defineProps<{ ticketId: string }>()
const emit = defineEmits<{ added: [comment: Comment] }>()

const body = ref('')
const saving = ref(false)
const error = ref<string | null>(null)

async function submit(): Promise<void> {
  if (!body.value.trim() || saving.value) return
  saving.value = true
  error.value = null
  try {
    emit('added', await commentsApi.add(props.ticketId, body.value))
    body.value = ''
  } catch (e) {
    if (e instanceof ApiError && e.status === 422) error.value = e.fields?.body ?? e.message
    else notify('error', e instanceof ApiError ? e.message : "Couldn't add the comment")
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <MarkdownEditor
    v-model="body"
    :saving="saving"
    :error="error"
    submit-label="Comment"
    placeholder="Add a comment (Ctrl+Enter to send)"
    label="New comment"
    :show-cancel="false"
    @submit="submit"
  />
</template>
