import { computed, onScopeDispose, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { Priority } from '@/api/types'
import {
  emptyFilters, FILTER_KEYS, parseFilters, serializeFilters, type BoardFilters, type QueryLike,
} from '@/lib/filters'
import { useBoardStore } from '@/stores/board'

const SEARCH_DEBOUNCE_MS = 200

/**
 * Board filters live in the URL query (`?q&priority&assignee&label`) and are mirrored into the board
 * store so `filteredColumns` and drag disabling follow them. Writes go through `router.replace`.
 */
export function useBoardFilters() {
  const route = useRoute()
  const router = useRouter()
  const board = useBoardStore()

  const filters = computed<BoardFilters>(() =>
    parseFilters(
      route.query as QueryLike,
      board.loadState === 'ready' ? { members: board.members, labels: board.labels } : undefined,
    ),
  )
  watch(filters, (f) => board.setFilters(f), { immediate: true })

  const searchText = ref(filters.value.q)
  watch(
    () => filters.value.q,
    (q) => {
      if (q !== searchText.value.trim()) searchText.value = q
    },
  )

  let timer: ReturnType<typeof setTimeout> | undefined
  onScopeDispose(() => clearTimeout(timer))

  function write(f: BoardFilters): void {
    const query: Record<string, unknown> = { ...route.query }
    for (const k of FILTER_KEYS) delete query[k]
    Object.assign(query, serializeFilters(f))
    void router.replace({ name: route.name ?? undefined, params: route.params, query } as never)
  }

  function update(patch: Partial<BoardFilters>): void {
    clearTimeout(timer)
    write({ ...filters.value, q: searchText.value.trim(), ...patch })
  }

  function setSearch(text: string): void {
    searchText.value = text
    clearTimeout(timer)
    timer = setTimeout(() => write({ ...filters.value, q: text.trim() }), SEARCH_DEBOUNCE_MS)
  }

  function clear(): void {
    clearTimeout(timer)
    searchText.value = ''
    write(emptyFilters())
  }

  return {
    filters,
    searchText,
    active: computed(() => board.filtersActive),
    setSearch,
    setPriority: (priority: Priority[]) => update({ priority }),
    setAssignee: (assignee: string | null) => update({ assignee }),
    setLabels: (label: string[]) => update({ label }),
    clear,
  }
}
