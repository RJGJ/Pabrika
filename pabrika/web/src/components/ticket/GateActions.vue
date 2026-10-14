<script setup lang="ts">
import { computed, ref } from 'vue'
import { ApiError } from '@/api/client'
import { decisions as decisionsApi } from '@/api/decisions'
import type { Decision, Gate, Verdict } from '@/api/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { Button } from '@/components/ui/button'
import { Textarea } from '@/components/ui/textarea'
import { useCan } from '@/composables/useCan'
import { notify } from '@/lib/toast'
import { classifyLabels, gateOf } from '@/lib/workflow'
import { useBoardStore } from '@/stores/board'

const NOTE_MAX = 2000
const GATE_NAME: Record<Gate, string> = { requirements: 'requirements', plan: 'plan' }

const props = defineProps<{ ticketId: string; ticketRef: string; labels: readonly { name: string }[] }>()
const emit = defineEmits<{
  /** The server recorded the decision. */
  recorded: [decision: Decision]
  /** 409 gate_not_open: the ticket moved on; the parent refetches it. */
  stale: []
}>()

const board = useBoardStore()
const { canEdit } = useCan()

const gate = computed(() => gateOf(classifyLabels(props.labels).stage))
// Editor or owner on a project that is not archived (viewers and archived projects see no buttons).
const visible = computed(() => !!gate.value && canEdit.value)

const open = ref(false)
const verdict = ref<Verdict>('approved')
const note = ref('')
const error = ref<string | null>(null)
const pending = ref(false)
// The gate the dialog was opened for (the label may change under an open dialog).
const dialogGate = ref<Gate>('requirements')

const approving = computed(() => verdict.value === 'approved')
const title = computed(() =>
  approving.value
    ? `Approve ${GATE_NAME[dialogGate.value]} for ${props.ticketRef}?`
    : `Request changes on the ${GATE_NAME[dialogGate.value]} for ${props.ticketRef}?`,
)

function start(v: Verdict): void {
  if (!gate.value) return
  verdict.value = v
  dialogGate.value = gate.value
  note.value = ''
  error.value = null
  open.value = true
}

async function submit(): Promise<void> {
  const n = note.value.trim()
  if (verdict.value === 'changes_requested' && !n) {
    error.value = 'A note is required when requesting changes'
    return
  }
  if (n.length > NOTE_MAX) {
    error.value = `The note can be at most ${NOTE_MAX} characters`
    return
  }
  pending.value = true
  error.value = null
  try {
    const d = await decisionsApi.create(props.ticketId, {
      gate: dialogGate.value,
      verdict: verdict.value,
      ...(n ? { note: n } : {}),
    })
    open.value = false
    notify('success', approving.value ? 'Approved' : 'Changes requested')
    emit('recorded', d)
  } catch (e) {
    if (!(e instanceof ApiError)) {
      error.value = "Couldn't record the decision"
    } else if (e.code === 'gate_not_open') {
      open.value = false
      notify('info', 'This ticket moved on')
      emit('stale')
    } else if (e.code === 'project_archived') {
      open.value = false
      notify('error', e.message)
      void board.refetchProject()
    } else if (e.status === 422) {
      error.value = e.fields?.note ?? e.message
    } else if (e.code === 'session_required') {
      error.value = e.message
    } else if (e.status !== 403) {
      error.value = e.message
    } else {
      open.value = false
    }
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <div v-if="visible" data-testid="gate-actions" class="flex flex-wrap items-center gap-2">
    <Button size="sm" @click="start('approved')">Approve</Button>
    <Button size="sm" variant="outline" @click="start('changes_requested')">Request changes</Button>
  </div>
  <ConfirmDialog
    v-model:open="open"
    :title="title"
    :description="approving ? 'The Orchestrator applies the next stage after you approve.' : 'Tell the agents what has to change.'"
    :confirm-label="approving ? 'Approve' : 'Request changes'"
    :pending="pending"
    @confirm="submit"
  >
    <div class="grid gap-1.5">
      <label for="gate-note" class="text-sm font-medium">
        Note <span class="font-normal text-muted-foreground">{{ approving ? '(optional)' : '(required)' }}</span>
      </label>
      <Textarea
        id="gate-note"
        v-model="note"
        :maxlength="NOTE_MAX"
        rows="4"
        :aria-invalid="error ? true : undefined"
        aria-describedby="gate-note-error"
      />
      <p v-if="error" id="gate-note-error" role="alert" class="text-xs text-destructive">{{ error }}</p>
    </div>
  </ConfirmDialog>
</template>
