import { computed } from 'vue'
import { useBoardStore } from '@/stores/board'

/** Role helpers for the open project. `canEdit` is false for viewers and for archived projects. */
export function useCan() {
  const board = useBoardStore()
  return {
    canEdit: computed(() => board.canEdit),
    isOwner: computed(() => board.isOwner),
    isViewer: computed(() => board.isViewer),
    isArchived: computed(() => board.isArchived),
  }
}
