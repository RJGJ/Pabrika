<script setup lang="ts">
import { computed } from 'vue'
import { SearchIcon, XIcon } from '@lucide/vue'
import { PRIORITIES, type Priority } from '@/api/types'
import LiveIndicator from '@/components/board/LiveIndicator.vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuLabel, DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useBoardFilters } from '@/composables/useBoardFilters'
import { useBoardStore } from '@/stores/board'

const board = useBoardStore()
const f = useBoardFilters()

const PRIORITY_LABEL: Record<Priority, string> = { low: 'Low', medium: 'Medium', high: 'High', urgent: 'Urgent' }

const assigneeValue = computed(() => f.filters.value.assignee ?? 'any')
const priorityCount = computed(() => f.filters.value.priority.length)
const labelCount = computed(() => f.filters.value.label.length)

function togglePriority(p: Priority, on: boolean): void {
  const cur = f.filters.value.priority
  f.setPriority(on ? [...cur.filter((x) => x !== p), p] : cur.filter((x) => x !== p))
}
function toggleLabel(id: string, on: boolean): void {
  const cur = f.filters.value.label
  f.setLabels(on ? [...cur.filter((x) => x !== id), id] : cur.filter((x) => x !== id))
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2" role="search" aria-label="Filter tickets">
    <div class="relative">
      <SearchIcon class="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
      <Input
        :model-value="f.searchText.value"
        placeholder="Search tickets"
        aria-label="Search tickets"
        class="h-9 w-44 pl-8"
        @update:model-value="f.setSearch(String($event))"
      />
    </div>

    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="outline" size="sm">Priority<span v-if="priorityCount"> ({{ priorityCount }})</span></Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start">
        <DropdownMenuLabel>Priority</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          v-for="p in PRIORITIES"
          :key="p"
          :model-value="f.filters.value.priority.includes(p)"
          @select.prevent
          @update:model-value="togglePriority(p, !!$event)"
        >{{ PRIORITY_LABEL[p] }}</DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>

    <Select
      :model-value="assigneeValue"
      @update:model-value="f.setAssignee($event === 'any' ? null : String($event))"
    >
      <SelectTrigger size="sm" class="w-40" aria-label="Filter by assignee"><SelectValue placeholder="Assignee" /></SelectTrigger>
      <SelectContent>
        <SelectItem value="any">Anyone</SelectItem>
        <SelectItem value="me">Me</SelectItem>
        <SelectItem value="none">Unassigned</SelectItem>
        <SelectItem v-for="m in board.members" :key="m.user.id" :value="m.user.id">{{ m.user.display_name }}</SelectItem>
      </SelectContent>
    </Select>

    <DropdownMenu v-if="board.labels.length">
      <DropdownMenuTrigger as-child>
        <Button variant="outline" size="sm">Labels<span v-if="labelCount"> ({{ labelCount }})</span></Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" class="max-h-72 overflow-y-auto">
        <DropdownMenuLabel>Labels</DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem
          v-for="l in board.labels"
          :key="l.id"
          :model-value="f.filters.value.label.includes(l.id)"
          @select.prevent
          @update:model-value="toggleLabel(l.id, !!$event)"
        >{{ l.name }}</DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>

    <Button v-if="f.active.value" variant="ghost" size="sm" @click="f.clear()"><XIcon /> Clear filters</Button>
    <span v-if="f.active.value && board.canEdit" class="text-xs text-muted-foreground" role="note">
      Clear filters to drag tickets
    </span>
    <div class="ml-auto"><LiveIndicator /></div>
  </div>
</template>
