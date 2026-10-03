<script setup lang="ts">
import { computed, ref } from 'vue'
import { CheckIcon, ChevronsUpDownIcon, PlusIcon } from '@lucide/vue'
import { ApiError } from '@/api/client'
import { labels as labelsApi } from '@/api/labels'
import type { Label } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { labelClasses } from '@/lib/colors'
import { cn } from '@/lib/utils'
import { notify } from '@/lib/toast'
import { useBoardStore } from '@/stores/board'

const props = defineProps<{ modelValue: string[]; labels: Label[]; disabled?: boolean; canCreate?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string[]] }>()
const board = useBoardStore()

const open = ref(false)
const search = ref('')
const creating = ref(false)

const selected = computed(() => props.labels.filter((l) => props.modelValue.includes(l.id)))
const trimmed = computed(() => search.value.trim())
const exact = computed(() => props.labels.find((l) => l.name.toLowerCase() === trimmed.value.toLowerCase()))
const showCreate = computed(() => !!props.canCreate && trimmed.value.length > 0 && trimmed.value.length <= 50 && !exact.value)

function toggle(id: string): void {
  search.value = ''
  emit('update:modelValue', props.modelValue.includes(id) ? props.modelValue.filter((x) => x !== id) : [...props.modelValue, id])
}

function select(id: string): void {
  if (!props.modelValue.includes(id)) emit('update:modelValue', [...props.modelValue, id])
}

async function create(): Promise<void> {
  const name = trimmed.value
  const key = board.project?.key
  if (!name || !key || creating.value) return
  creating.value = true
  try {
    const l = await labelsApi.create(key, name, 'gray')
    if (!board.labels.some((x) => x.id === l.id)) board.labels = [...board.labels, l]
    select(l.id)
    search.value = ''
  } catch (e) {
    if (e instanceof ApiError && e.code === 'label_exists') {
      // Someone made it first: select the existing label instead.
      let existing = board.labels.find((x) => x.name.toLowerCase() === name.toLowerCase())
      if (!existing) {
        board.labels = await labelsApi.list(key)
        existing = board.labels.find((x) => x.name.toLowerCase() === name.toLowerCase())
      }
      if (existing) select(existing.id)
      search.value = ''
    } else {
      notify('error', e instanceof ApiError ? e.message : "Couldn't create the label")
    }
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="outline" role="combobox" :aria-expanded="open" aria-label="Labels" :disabled="disabled" class="h-auto min-h-9 w-full justify-between">
        <span class="flex flex-wrap gap-1">
          <span v-if="selected.length === 0" class="text-muted-foreground">No labels</span>
          <span
            v-for="l in selected"
            :key="l.id"
            :class="cn('rounded-full px-2 py-0.5 text-[11px]', labelClasses(l.color))"
          >{{ l.name }}</span>
        </span>
        <ChevronsUpDownIcon class="opacity-50" />
      </Button>
    </PopoverTrigger>
    <PopoverContent class="w-64 p-0" align="start">
      <Command>
        <CommandInput
          placeholder="Search or create a label"
          @input="search = ($event.target as HTMLInputElement).value"
        />
        <CommandList>
          <CommandEmpty v-if="!showCreate">No labels found.</CommandEmpty>
          <CommandGroup>
            <CommandItem v-for="l in labels" :key="l.id" :value="l.id" @select="toggle(l.id)">
              <CheckIcon :class="modelValue.includes(l.id) ? 'opacity-100' : 'opacity-0'" />
              {{ l.name }}
            </CommandItem>
            <!-- Keyed by the typed name: Command captures an item's text once, on mount, for filtering, so the
                 item must remount per keystroke or it is filtered out by its stale text. -->
            <CommandItem v-if="showCreate" :key="`create:${trimmed}`" value="__create__" :disabled="creating" @select="create">
              <PlusIcon /> Create label "{{ trimmed }}"
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
