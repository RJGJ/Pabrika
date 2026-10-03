<script setup lang="ts">
import { computed, ref } from 'vue'
import { CheckIcon, ChevronsUpDownIcon } from '@lucide/vue'
import type { Member } from '@/api/types'
import UserAvatar from '@/components/common/UserAvatar.vue'
import { Button } from '@/components/ui/button'
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from '@/components/ui/command'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'

const props = defineProps<{ modelValue: string | null; members: Member[]; disabled?: boolean }>()
const emit = defineEmits<{ 'update:modelValue': [value: string | null] }>()

const open = ref(false)
const NONE = '__unassigned__'
const current = computed(() => props.members.find((m) => m.user.id === props.modelValue)?.user ?? null)

function choose(value: unknown): void {
  open.value = false
  const id = value === NONE ? null : String(value)
  if (id !== props.modelValue) emit('update:modelValue', id)
}
</script>

<template>
  <Popover v-model:open="open">
    <PopoverTrigger as-child>
      <Button variant="outline" role="combobox" :aria-expanded="open" aria-label="Assignee" :disabled="disabled" class="w-full justify-between">
        <span class="flex min-w-0 items-center gap-2">
          <UserAvatar v-if="current" :name="current.display_name" />
          <span class="truncate">{{ current ? current.display_name : 'Unassigned' }}</span>
        </span>
        <ChevronsUpDownIcon class="opacity-50" />
      </Button>
    </PopoverTrigger>
    <PopoverContent class="w-64 p-0" align="start">
      <Command>
        <CommandInput placeholder="Search members" />
        <CommandList>
          <CommandEmpty>No member found.</CommandEmpty>
          <CommandGroup>
            <CommandItem :value="NONE" @select="choose(NONE)">
              <CheckIcon :class="modelValue === null ? 'opacity-100' : 'opacity-0'" />
              Unassigned
            </CommandItem>
            <CommandItem v-for="m in members" :key="m.user.id" :value="m.user.id" @select="choose(m.user.id)">
              <CheckIcon :class="modelValue === m.user.id ? 'opacity-100' : 'opacity-0'" />
              {{ m.user.display_name }}
            </CommandItem>
          </CommandGroup>
        </CommandList>
      </Command>
    </PopoverContent>
  </Popover>
</template>
