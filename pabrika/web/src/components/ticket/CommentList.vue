<script setup lang="ts">
import { ref } from 'vue'
import { ApiError } from '@/api/client'
import { comments as commentsApi } from '@/api/comments'
import type { Comment } from '@/api/types'
import BotBadge from '@/components/common/BotBadge.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import MarkdownEditor from '@/components/ticket/MarkdownEditor.vue'
import MarkdownView from '@/components/ticket/MarkdownView.vue'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { relativeTime } from '@/lib/dates'
import { notify } from '@/lib/toast'

const props = defineProps<{
  comments: Comment[]
  /** Editor or owner on a project that is not archived. */
  canEdit: boolean
  isOwner: boolean
  meId: string | null
}>()
const emit = defineEmits<{ updated: [comment: Comment]; deleted: [id: string]; refetch: [] }>()

const editingId = ref<string | null>(null)
const draft = ref('')
const saving = ref(false)
const editError = ref<string | null>(null)
const deleting = ref<Comment | null>(null)
const deleteOpen = ref(false)

const isMine = (c: Comment) => c.author.type === 'user' && c.author.id === props.meId
/** Own comments only; a human can never edit an agent's comment. */
const canEditComment = (c: Comment) => props.canEdit && isMine(c)
/** The author, or a project owner for any comment. */
const canDeleteComment = (c: Comment) => props.canEdit && (isMine(c) || props.isOwner)

function startEdit(c: Comment): void {
  editingId.value = c.id
  draft.value = c.body
  editError.value = null
}

async function saveEdit(c: Comment): Promise<void> {
  saving.value = true
  editError.value = null
  try {
    emit('updated', await commentsApi.update(c.id, draft.value))
    editingId.value = null
  } catch (e) {
    if (e instanceof ApiError && e.status === 422) editError.value = e.fields?.body ?? e.message
    else {
      notify('error', e instanceof ApiError ? e.message : "Couldn't save the comment")
      if (e instanceof ApiError && (e.status === 403 || e.status === 409)) emit('refetch')
    }
  } finally {
    saving.value = false
  }
}

function askDelete(c: Comment): void {
  deleting.value = c
  deleteOpen.value = true
}

async function confirmDelete(): Promise<void> {
  const c = deleting.value
  if (!c) return
  saving.value = true
  try {
    await commentsApi.remove(c.id)
    emit('deleted', c.id)
    deleteOpen.value = false
  } catch (e) {
    notify('error', e instanceof ApiError ? e.message : "Couldn't delete the comment")
    if (e instanceof ApiError && (e.status === 403 || e.status === 409 || e.status === 404)) {
      deleteOpen.value = false
      emit('refetch')
    }
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <TooltipProvider>
    <ul class="grid gap-4" aria-label="Comments">
      <li v-for="c in comments" :key="c.id" :data-comment-id="c.id" class="grid gap-1.5">
        <div class="flex flex-wrap items-center gap-2 text-sm">
          <UserAvatar :name="c.author.name" />
          <span class="font-medium">{{ c.author.name }}</span>
          <Tooltip v-if="c.author.bot">
            <TooltipTrigger as-child><span><BotBadge /></span></TooltipTrigger>
            <TooltipContent>API token{{ c.author.owner_name ? ` owned by ${c.author.owner_name}` : '' }}</TooltipContent>
          </Tooltip>
          <time class="text-xs text-muted-foreground" :datetime="c.created_at" :title="c.created_at">
            {{ relativeTime(c.created_at) }}
          </time>
          <span v-if="c.edited_at" class="text-xs text-muted-foreground">edited</span>
          <span class="ml-auto flex gap-1">
            <Button v-if="canEditComment(c) && editingId !== c.id" variant="ghost" size="xs" @click="startEdit(c)">Edit</Button>
            <Button v-if="canDeleteComment(c) && editingId !== c.id" variant="ghost" size="xs" @click="askDelete(c)">Delete</Button>
          </span>
        </div>
        <MarkdownEditor
          v-if="editingId === c.id"
          v-model="draft"
          :saving="saving"
          :error="editError"
          label="Edit comment"
          @submit="saveEdit(c)"
          @cancel="editingId = null"
        />
        <MarkdownView v-else :source="c.body" />
      </li>
    </ul>
  </TooltipProvider>
  <ConfirmDialog
    v-model:open="deleteOpen"
    title="Delete comment?"
    description="This can't be undone."
    confirm-label="Delete"
    destructive
    :pending="saving"
    @confirm="confirmDelete"
  />
</template>
