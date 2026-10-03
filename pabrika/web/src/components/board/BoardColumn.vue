<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { VueDraggable, type DraggableEvent } from 'vue-draggable-plus'
import type { Status } from '@/api/types'
import InlineAddTicket from '@/components/board/InlineAddTicket.vue'
import TicketCard from '@/components/board/TicketCard.vue'
import { computePlacement, isNoopDrop } from '@/lib/placement'
import { isTempId, useBoardStore } from '@/stores/board'

const props = defineProps<{ status: Status; title: string }>()
const board = useBoardStore()

/**
 * Sortable is bound to a LOCAL copy of the column's ids. It never touches store state during a drag;
 * the copy is re-synced from the store when no drag is active, after a rollback or reload (key bump).
 */
const storeIds = computed(() => board.filteredColumns[props.status])
const localIds = ref<string[]>([...storeIds.value])
const sameIds = (a: readonly string[], b: readonly string[]) => a.length === b.length && a.every((x, i) => x === b[i])

function resync(): void {
  if (!sameIds(storeIds.value, localIds.value)) localIds.value = [...storeIds.value]
}
watch(storeIds, () => {
  if (!board.dragging) resync()
})
watch(
  () => board.dragging,
  (d) => {
    if (!d) resync()
  },
)
watch(
  () => board.columnKeys[props.status],
  () => resync(),
)

const disabled = computed(() => !board.canEdit || board.filtersActive)
const total = computed(() => board.counts[props.status])
const visible = computed(() => board.filteredCounts[props.status])
const countLabel = computed(() => (board.filtersActive ? `${visible.value} of ${total.value}` : String(total.value)))

function placementFor(movedId: string, newIndex: number) {
  // Neighbours must be real tickets currently in the target column (temp cards are never neighbours).
  const ids = localIds.value.filter((id) => id !== movedId && !isTempId(id))
  return computePlacement(ids, newIndex)
}

function onStart(): void {
  board.startDrag(props.status)
}

/** Cross-column drop, handled on the target list. */
function onAdd(evt: DraggableEvent<string>): void {
  const id = evt.data
  if (!id) return
  void board.moveTicket(id, props.status, placementFor(id, evt.newDraggableIndex ?? evt.newIndex ?? 0))
}

/** Same-column reorder. */
function onUpdate(evt: DraggableEvent<string>): void {
  const id = evt.data
  const newIndex = evt.newDraggableIndex ?? evt.newIndex ?? 0
  const oldIndex = evt.oldDraggableIndex ?? evt.oldIndex ?? 0
  if (!id || isNoopDrop(props.status, props.status, oldIndex, newIndex)) return
  void board.moveTicket(id, props.status, placementFor(id, newIndex))
}

function onEnd(): void {
  board.endDrag()
}
</script>

<template>
  <section
    :aria-labelledby="`col-${status}`"
    class="flex max-h-full min-w-64 flex-1 flex-col rounded-lg border bg-muted/40"
    :data-column="status"
  >
    <header class="flex items-center justify-between px-3 py-2">
      <h2 :id="`col-${status}`" class="text-sm font-semibold">{{ title }}</h2>
      <span class="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground" :aria-label="`${countLabel} tickets`">
        {{ countLabel }}
      </span>
    </header>
    <div class="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
      <VueDraggable
        :key="board.columnKeys[status]"
        v-model="localIds"
        group="tickets"
        :disabled="disabled"
        draggable="[data-draggable]"
        :animation="150"
        :force-fallback="true"
        :fallback-tolerance="4"
        :delay="120"
        :delay-on-touch-only="true"
        class="flex min-h-12 flex-col gap-2"
        @start="onStart"
        @add="onAdd"
        @update="onUpdate"
        @end="onEnd"
      >
        <TicketCard v-for="id in localIds" :id="id" :key="id" />
      </VueDraggable>
      <p v-if="localIds.length === 0" class="px-1 py-2 text-center text-xs text-muted-foreground">
        {{ board.filtersActive ? 'No matching tickets' : board.canEdit ? 'No tickets' : 'No tickets yet' }}
      </p>
    </div>
    <div v-if="board.canEdit" class="px-2 pb-2">
      <InlineAddTicket :status="status" />
    </div>
  </section>
</template>
