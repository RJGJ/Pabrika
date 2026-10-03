<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Trash2Icon } from '@lucide/vue'
import { ApiError } from '@/api/client'
import type { Priority, Status, UpdateTicketInput } from '@/api/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import ActivityList from '@/components/ticket/ActivityList.vue'
import AssigneePicker from '@/components/ticket/AssigneePicker.vue'
import CommentForm from '@/components/ticket/CommentForm.vue'
import CommentList from '@/components/ticket/CommentList.vue'
import LabelPicker from '@/components/ticket/LabelPicker.vue'
import MarkdownEditor from '@/components/ticket/MarkdownEditor.vue'
import MarkdownView from '@/components/ticket/MarkdownView.vue'
import PrioritySelect from '@/components/ticket/PrioritySelect.vue'
import StatusSelect from '@/components/ticket/StatusSelect.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useCan } from '@/composables/useCan'
import { useTicketDetail } from '@/composables/useTicketDetail'
import { statusName } from '@/lib/activity'
import { labelClasses, priorityClasses } from '@/lib/colors'
import { formatDueDate } from '@/lib/dates'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { isTempId, useBoardStore } from '@/stores/board'
import { useAuthStore } from '@/stores/auth'

const route = useRoute()
const router = useRouter()
const board = useBoardStore()
const auth = useAuthStore()
const { canEdit, isOwner } = useCan()

const numberParam = computed(() => String(route.params.number ?? ''))
const validNumber = computed(() => /^[1-9]\d*$/.test(numberParam.value))

/** The ULID when the ticket is on the board, else the KEY-n reference (cold deep link); fixed per number. */
const target = ref<string | null>(null)
watch(
  () => (validNumber.value ? Number(numberParam.value) : null),
  (num) => {
    if (num === null || !board.project) {
      target.value = null
      return
    }
    target.value = board.ticketByNumber(num)?.id ?? `${board.project.key}-${num}`
  },
  { immediate: true },
)

const detail = useTicketDetail(target)
const ticketId = computed(() => detail.ticket.value?.id ?? (target.value && !target.value.includes('-') ? target.value : null))
const summary = computed(() => (ticketId.value ? board.ticketsById[ticketId.value] : undefined))
/** Board copy (optimistic, shown immediately) with the fetched copy as the fallback. */
const view = computed(() => summary.value ?? detail.ticket.value ?? undefined)
const notFound = computed(() => !validNumber.value || detail.ticketState.value === 'not-found')

// A ticket reached by link that the board does not hold yet is added to the board.
watch(
  () => detail.ticket.value,
  (t) => {
    if (t && board.ticketsLoaded && !board.ticketsById[t.id] && !isTempId(t.id)) board.adopt(t)
  },
)

// ---- drafts ----
const titleDraft = ref(view.value?.title ?? '')
const titleError = ref<string | null>(null)
const titleRemote = ref(false)
const descEditing = ref(false)
const descDraft = ref('')
const descError = ref<string | null>(null)
const descSaving = ref(false)
const descRemote = ref(false)

watch(
  () => detail.ticket.value,
  (nt, old) => {
    if (!nt) return
    if (!old || old.id !== nt.id) {
      titleDraft.value = nt.title
      descDraft.value = nt.description
      descEditing.value = false
      titleRemote.value = descRemote.value = false
      return
    }
    if (nt.title !== old.title && nt.title !== titleDraft.value) {
      if (titleDraft.value === old.title) titleDraft.value = nt.title
      else titleRemote.value = true
    }
    if (nt.description !== old.description && nt.description !== descDraft.value) {
      if (descEditing.value && descDraft.value !== old.description) descRemote.value = true
      else descDraft.value = nt.description
    }
  },
)

/** Escape reverts an edited title first; with nothing to revert it falls through and closes the panel. */
function onTitleEscape(e: KeyboardEvent): void {
  const current = detail.ticket.value?.title ?? view.value?.title ?? ''
  if (titleDraft.value === current) return
  e.preventDefault()
  reloadTitle()
}

function reloadTitle(): void {
  titleDraft.value = detail.ticket.value?.title ?? view.value?.title ?? ''
  titleRemote.value = false
  titleError.value = null
}
function reloadDescription(): void {
  descDraft.value = detail.ticket.value?.description ?? ''
  descRemote.value = false
}

async function saveTitle(): Promise<void> {
  const t = view.value
  if (!t || !canEdit.value) return
  const v = titleDraft.value.trim()
  if (!v) {
    titleError.value = 'Title is required'
    return
  }
  titleError.value = null
  if (v === t.title) return
  try {
    await board.updateTicket(t.id, { title: v })
    titleDraft.value = v
  } catch (e) {
    if (e instanceof ApiError && e.status === 422) titleError.value = e.fields?.title ?? e.message
    else titleDraft.value = board.ticketsById[t.id]?.title ?? t.title // rolled back
  }
}

function startEditDescription(): void {
  descDraft.value = detail.ticket.value?.description ?? ''
  descError.value = null
  descRemote.value = false
  descEditing.value = true
}

async function saveDescription(): Promise<void> {
  const t = view.value
  if (!t) return
  descSaving.value = true
  descError.value = null
  try {
    await board.updateTicket(t.id, { description: descDraft.value })
    descEditing.value = false
    descRemote.value = false
  } catch (e) {
    if (e instanceof ApiError && e.status === 422) descError.value = e.fields?.description ?? e.message
  } finally {
    descSaving.value = false
  }
}

/** Property edits: optimistic with rollback (the store does both); a 422 is toasted here. */
async function patch(p: UpdateTicketInput): Promise<void> {
  const t = view.value
  if (!t) return
  try {
    await board.updateTicket(t.id, p)
  } catch (e) {
    if (e instanceof ApiError && e.status === 422) notify('error', Object.values(e.fields ?? {})[0] ?? e.message)
  }
}

function onStatus(s: Status): void {
  const t = view.value
  if (t && s !== t.status) void board.moveTicket(t.id, s, { place: 'bottom' })
}

// ---- comments ----
function onCommentAdded(c: Parameters<typeof detail.upsertComment>[0]): void {
  detail.upsertComment(c) // idempotent with the own comment.added event
}

// ---- delete ----
const deleteOpen = ref(false)
const deleting = ref(false)
async function confirmDelete(): Promise<void> {
  const t = view.value
  if (!t) return
  deleting.value = true
  try {
    await board.deleteTicket(t.id)
    deleteOpen.value = false
    close()
  } catch {
    deleteOpen.value = false
  } finally {
    deleting.value = false
  }
}

// ---- closing ----
function close(): void {
  const dest = router.resolve({ name: 'board', params: { key: board.project?.key ?? String(route.params.key) }, query: route.query })
  if (window.history.state?.back === dest.fullPath) router.back()
  else void router.push(dest)
}

/** Return focus to the originating card, or to the board heading after a cold deep link. */
function onCloseAutoFocus(e: Event): void {
  const ref = board.selectedRef
  const card = ref ? document.querySelector<HTMLElement>(`[data-ticket-ref="${ref}"]`) : null
  const el = card ?? document.querySelector<HTMLElement>('h1')
  if (el) {
    e.preventDefault()
    el.focus()
  }
}

const PRIORITY_LABEL: Record<string, string> = { low: 'Low', medium: 'Medium', high: 'High', urgent: 'Urgent' }
</script>

<template>
  <Sheet :open="true" @update:open="(v: boolean) => !v && close()">
    <SheetContent class="w-full gap-0 overflow-y-auto p-0 sm:max-w-xl" @close-auto-focus="onCloseAutoFocus">
      <SheetHeader class="border-b p-4 pr-12">
        <SheetTitle class="flex items-baseline gap-2">
          <span class="font-mono text-sm text-muted-foreground">
            {{ view?.ref ?? (validNumber ? `${board.project?.key}-${numberParam}` : 'Ticket') }}
          </span>
        </SheetTitle>
        <SheetDescription class="sr-only">Ticket details</SheetDescription>
      </SheetHeader>

      <div class="p-4">
        <EmptyState v-if="notFound" title="Ticket not found" description="It may have been deleted, or the link is wrong.">
          <Button @click="close">Close</Button>
        </EmptyState>

        <div v-else-if="!view && detail.ticketState.value === 'error'" class="grid gap-2" role="alert">
          <p class="text-sm text-destructive">Couldn't load this ticket.</p>
          <div><Button variant="outline" size="sm" @click="detail.retryTicket()">Retry</Button></div>
        </div>

        <div v-else-if="!view" class="grid gap-3" role="status" aria-label="Loading ticket">
          <Skeleton class="h-8 w-3/4" />
          <Skeleton class="h-24 w-full" />
        </div>

        <div v-else class="grid gap-5">
          <!-- Title -->
          <div class="grid gap-1.5">
            <template v-if="canEdit">
              <Label for="ticket-title" class="sr-only">Title</Label>
              <Input
                id="ticket-title"
                v-model="titleDraft"
                :maxlength="200"
                class="h-10 text-lg font-semibold"
                :aria-invalid="titleError ? true : undefined"
                @blur="saveTitle"
                @keydown.enter.prevent="($event.target as HTMLInputElement).blur()"
                @keydown.esc="onTitleEscape"
              />
              <p v-if="titleError" role="alert" class="text-xs text-destructive">{{ titleError }}</p>
              <Alert v-if="titleRemote">
                <AlertDescription class="flex items-center gap-2">
                  Changed by someone else.
                  <Button variant="link" size="sm" class="h-auto p-0" @click="reloadTitle">Reload</Button>
                </AlertDescription>
              </Alert>
            </template>
            <h2 v-else class="text-lg font-semibold break-words">{{ view.title }}</h2>
          </div>

          <!-- Properties -->
          <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div class="grid gap-1.5">
              <span class="text-xs font-medium text-muted-foreground">Status</span>
              <StatusSelect v-if="canEdit" :model-value="view.status" @update:model-value="onStatus" />
              <span v-else class="text-sm">{{ statusName(view.status) }}</span>
            </div>
            <div class="grid gap-1.5">
              <span class="text-xs font-medium text-muted-foreground">Priority</span>
              <PrioritySelect v-if="canEdit" :model-value="view.priority" @update:model-value="(p: Priority) => patch({ priority: p })" />
              <span v-else :class="cn('w-fit rounded px-1.5 py-0.5 text-sm font-medium', priorityClasses(view.priority))">
                {{ PRIORITY_LABEL[view.priority] }}
              </span>
            </div>
            <div class="grid gap-1.5">
              <span class="text-xs font-medium text-muted-foreground">Assignee</span>
              <AssigneePicker
                v-if="canEdit"
                :model-value="view.assignee?.id ?? null"
                :members="board.members"
                @update:model-value="(id: string | null) => patch({ assignee: id })"
              />
              <span v-else-if="view.assignee" class="flex items-center gap-2 text-sm">
                <UserAvatar :name="view.assignee.display_name" /> {{ view.assignee.display_name }}
              </span>
              <span v-else class="text-sm text-muted-foreground">Unassigned</span>
            </div>
            <div class="grid gap-1.5">
              <Label for="ticket-due" class="text-xs font-medium text-muted-foreground">Due date</Label>
              <div v-if="canEdit" class="flex gap-2">
                <Input
                  id="ticket-due"
                  type="date"
                  :model-value="view.due_date ?? ''"
                  @change="patch({ due_date: ($event.target as HTMLInputElement).value || null })"
                />
                <Button v-if="view.due_date" variant="ghost" size="sm" @click="patch({ due_date: null })">Clear</Button>
              </div>
              <span v-else class="text-sm" :class="view.due_date ? '' : 'text-muted-foreground'">
                {{ view.due_date ? formatDueDate(view.due_date) : 'No due date' }}
              </span>
            </div>
            <div class="grid gap-1.5 sm:col-span-2">
              <span class="text-xs font-medium text-muted-foreground">Labels</span>
              <LabelPicker
                v-if="canEdit"
                :model-value="view.labels.map((l) => l.id)"
                :labels="board.labels"
                :can-create="canEdit"
                @update:model-value="(ids: string[]) => patch({ labels: ids })"
              />
              <div v-else-if="view.labels.length" class="flex flex-wrap gap-1">
                <span v-for="l in view.labels" :key="l.id" :class="cn('rounded-full px-2 py-0.5 text-[11px]', labelClasses(l.color))">
                  {{ l.name }}
                </span>
              </div>
              <span v-else class="text-sm text-muted-foreground">No labels</span>
            </div>
          </div>

          <!-- Description -->
          <section class="grid gap-2" aria-labelledby="ticket-desc-h">
            <div class="flex items-center justify-between">
              <h3 id="ticket-desc-h" class="text-sm font-semibold">Description</h3>
              <Button v-if="canEdit && !descEditing && detail.ticketState.value === 'ready'" variant="ghost" size="xs" @click="startEditDescription">
                Edit
              </Button>
            </div>
            <template v-if="detail.ticketState.value === 'loading' || detail.ticketState.value === 'idle'">
              <Skeleton class="h-16 w-full" />
            </template>
            <p v-else-if="detail.ticketState.value === 'error'" class="text-sm text-destructive" role="alert">
              Couldn't load the description.
              <Button variant="link" size="sm" class="h-auto p-0" @click="detail.retryTicket()">Retry</Button>
            </p>
            <template v-else-if="descEditing">
              <Alert v-if="descRemote">
                <AlertDescription class="flex items-center gap-2">
                  Changed by someone else. Saving will overwrite their change.
                  <Button variant="link" size="sm" class="h-auto p-0" @click="reloadDescription">Reload</Button>
                </AlertDescription>
              </Alert>
              <MarkdownEditor
                v-model="descDraft"
                :saving="descSaving"
                :error="descError"
                label="Description"
                placeholder="Describe the ticket in Markdown"
                allow-empty
                @submit="saveDescription"
                @cancel="descEditing = false"
              />
            </template>
            <template v-else>
              <MarkdownView v-if="detail.ticket.value?.description" :source="detail.ticket.value.description" />
              <p v-else class="text-sm text-muted-foreground">No description.</p>
            </template>
          </section>

          <!-- Comments and activity -->
          <Tabs default-value="comments">
            <TabsList>
              <TabsTrigger value="comments">Comments<span v-if="detail.comments.value.length"> ({{ detail.comments.value.length }})</span></TabsTrigger>
              <TabsTrigger value="activity">Activity</TabsTrigger>
            </TabsList>
            <TabsContent value="comments" class="grid gap-4 pt-3">
              <div v-if="detail.commentsState.value === 'loading' || detail.commentsState.value === 'idle'" class="grid gap-2">
                <Skeleton class="h-10 w-full" /><Skeleton class="h-10 w-full" />
              </div>
              <p v-else-if="detail.commentsState.value === 'error'" class="text-sm text-destructive" role="alert">
                Couldn't load comments.
                <Button variant="link" size="sm" class="h-auto p-0" @click="detail.retryComments()">Retry</Button>
              </p>
              <template v-else>
                <p v-if="detail.comments.value.length === 0" class="text-sm text-muted-foreground">No comments yet.</p>
                <CommentList
                  :comments="detail.comments.value"
                  :can-edit="canEdit"
                  :is-owner="isOwner"
                  :me-id="auth.user?.id ?? null"
                  @updated="detail.upsertComment"
                  @deleted="detail.removeComment"
                  @refetch="detail.retryComments()"
                />
              </template>
              <CommentForm v-if="canEdit && ticketId" :ticket-id="ticketId" @added="onCommentAdded" />
            </TabsContent>
            <TabsContent value="activity" class="pt-3">
              <div v-if="detail.activityState.value === 'loading' || detail.activityState.value === 'idle'" class="grid gap-2">
                <Skeleton class="h-5 w-full" /><Skeleton class="h-5 w-2/3" />
              </div>
              <p v-else-if="detail.activityState.value === 'error'" class="text-sm text-destructive" role="alert">
                Couldn't load activity.
                <Button variant="link" size="sm" class="h-auto p-0" @click="detail.retryActivity()">Retry</Button>
              </p>
              <ActivityList
                v-else
                :items="detail.activity.value"
                :members="board.members"
                :has-more="!!detail.activityNext.value"
                :loading-more="detail.activityMore.value"
                @load-more="detail.loadMoreActivity()"
              />
            </TabsContent>
          </Tabs>

          <div v-if="canEdit" class="border-t pt-4">
            <Button variant="outline" size="sm" class="text-destructive" @click="deleteOpen = true">
              <Trash2Icon /> Delete ticket
            </Button>
          </div>
        </div>
      </div>
    </SheetContent>
  </Sheet>

  <ConfirmDialog
    v-model:open="deleteOpen"
    :title="`Delete ${view?.ref ?? 'ticket'}?`"
    description="The ticket and its comments will be removed."
    confirm-label="Delete"
    destructive
    :pending="deleting"
    @confirm="confirmDelete"
  />
</template>
