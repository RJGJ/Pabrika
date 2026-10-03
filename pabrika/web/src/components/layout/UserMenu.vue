<script setup lang="ts">
import { ChevronsUpDownIcon, LogOutIcon, SettingsIcon } from '@lucide/vue'
import { RouterLink } from 'vue-router'
import UserAvatar from '@/components/common/UserAvatar.vue'
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuRadioGroup,
  DropdownMenuRadioItem, DropdownMenuSeparator, DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from '@/components/ui/sidebar'
import { useAuthStore } from '@/stores/auth'
import { useUiStore } from '@/stores/ui'
import type { Theme } from '@/lib/theme'

const auth = useAuthStore()
const ui = useUiStore()
</script>

<template>
  <SidebarMenu v-if="auth.user">
    <SidebarMenuItem>
      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <SidebarMenuButton size="lg" aria-label="User menu">
            <UserAvatar :name="auth.user.display_name" class="size-8 text-xs" />
            <div class="grid flex-1 text-left text-sm leading-tight">
              <span class="truncate font-medium">{{ auth.user.display_name }}</span>
              <span class="truncate text-xs text-muted-foreground">{{ auth.user.email }}</span>
            </div>
            <ChevronsUpDownIcon class="ml-auto size-4" />
          </SidebarMenuButton>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" class="w-56">
          <DropdownMenuLabel class="truncate">{{ auth.user.display_name }}</DropdownMenuLabel>
          <DropdownMenuItem as-child>
            <RouterLink :to="{ name: 'account' }"><SettingsIcon class="mr-2 size-4" />Account settings</RouterLink>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuLabel class="text-xs text-muted-foreground">Theme</DropdownMenuLabel>
          <DropdownMenuRadioGroup :model-value="ui.theme" @update:model-value="ui.setTheme($event as Theme)">
            <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="system">System</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem @select="auth.logout()"><LogOutIcon class="mr-2 size-4" />Log out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </SidebarMenuItem>
  </SidebarMenu>
</template>
