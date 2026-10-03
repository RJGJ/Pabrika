<script setup lang="ts">
import { MonitorIcon, MoonIcon, SunIcon } from '@lucide/vue'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuRadioGroup, DropdownMenuRadioItem, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { useUiStore } from '@/stores/ui'
import type { Theme } from '@/lib/theme'

const ui = useUiStore()
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" aria-label="Change theme">
        <SunIcon class="dark:hidden" />
        <MoonIcon class="hidden dark:block" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end">
      <DropdownMenuRadioGroup :model-value="ui.theme" @update:model-value="ui.setTheme($event as Theme)">
        <DropdownMenuRadioItem value="light"><SunIcon class="mr-2 size-4" />Light</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="dark"><MoonIcon class="mr-2 size-4" />Dark</DropdownMenuRadioItem>
        <DropdownMenuRadioItem value="system"><MonitorIcon class="mr-2 size-4" />System</DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
