<script setup lang="ts">
import { ref } from 'vue'
import type { Member, Role } from '@/api/types'
import { ROLES } from '@/api/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import FormField from '@/components/common/FormField.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { mapFormError } from '@/lib/forms'
import { notify } from '@/lib/toast'
import { useAuthStore } from '@/stores/auth'
import { useProjectSettingsStore } from '@/stores/projectSettings'
import NativeSelect from './NativeSelect.vue'

const store = useProjectSettingsStore()
const auth = useAuthStore()

const roleOptions = ROLES.map((r) => ({ value: r, label: r.charAt(0).toUpperCase() + r.slice(1) }))

const email = ref('')
const newRole = ref<Role>('viewer')
const addErrors = ref<Record<string, string>>({})
const addFormError = ref<string | null>(null)
const adding = ref(false)

const removeTarget = ref<Member | null>(null)
const removeOpen = ref(false)
const removePending = ref(false)

const leaveOpen = ref(false)
const leavePending = ref(false)

const isMe = (m: Member) => m.user.id === auth.user?.id

async function add() {
  addFormError.value = null
  addErrors.value = {}
  const value = email.value.trim()
  if (!value || !value.includes('@')) {
    addErrors.value = { email: 'Enter a valid email address' }
    return
  }
  adding.value = true
  try {
    await store.addMember(value, newRole.value)
    email.value = ''
    notify('success', 'Member added')
  } catch (e) {
    const m = mapFormError(e, { already_member: 'email' })
    addErrors.value = m.fields
    addFormError.value = m.form
  } finally {
    adding.value = false
  }
}

async function changeRole(m: Member, role: string) {
  if (role === m.role) return
  try {
    await store.setMemberRole(m.user.id, role as Role)
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not change the role')
    await store.reloadMembers() // snap the select back to the server value
  }
}

function askRemove(m: Member) {
  removeTarget.value = m
  removeOpen.value = true
}

async function doRemove() {
  const m = removeTarget.value
  if (!m) return
  removePending.value = true
  try {
    if (isMe(m)) await store.leave()
    else await store.removeMember(m.user.id)
    removeOpen.value = false
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not remove the member')
    removeOpen.value = false
  } finally {
    removePending.value = false
  }
}

async function doLeave() {
  leavePending.value = true
  try {
    await store.leave()
    leaveOpen.value = false
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not leave the project')
    leaveOpen.value = false
  } finally {
    leavePending.value = false
  }
}
</script>

<template>
  <div class="grid gap-6" data-testid="members">
    <form
      v-if="store.canManageProject"
      class="grid max-w-xl gap-3 sm:grid-cols-[1fr_auto_auto] sm:items-start"
      novalidate
      data-testid="add-member-form"
      @submit.prevent="add"
    >
      <FormField id="member-email" label="Add member by email" :error="addErrors.email">
        <Input id="member-email" v-model="email" type="email" autocomplete="off" placeholder="name@example.com" />
      </FormField>
      <FormField id="member-role" label="Role" :error="addErrors.role">
        <NativeSelect id="member-role" v-model="newRole" :options="roleOptions" />
      </FormField>
      <div class="sm:pt-[22px]">
        <Button type="submit" :disabled="adding">Add</Button>
      </div>
      <p v-if="addFormError" role="alert" class="text-sm text-destructive sm:col-span-3">{{ addFormError }}</p>
    </form>

    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Member</TableHead>
          <TableHead>Email</TableHead>
          <TableHead class="w-40">Role</TableHead>
          <TableHead v-if="store.canManageProject" class="w-28"><span class="sr-only">Actions</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow v-for="m in store.members" :key="m.user.id" :data-testid="`member-${m.user.id}`">
          <TableCell>
            <div class="flex items-center gap-2">
              <UserAvatar :name="m.user.display_name" />
              <span>{{ m.user.display_name }}</span>
              <Badge v-if="isMe(m)" variant="secondary">You</Badge>
            </div>
          </TableCell>
          <TableCell class="text-muted-foreground">{{ m.user.email }}</TableCell>
          <TableCell>
            <NativeSelect
              v-if="store.canManageProject"
              :model-value="m.role"
              :options="roleOptions"
              :aria-label="`Role for ${m.user.display_name}`"
              @update:model-value="(v) => changeRole(m, v)"
            />
            <span v-else class="capitalize">{{ m.role }}</span>
          </TableCell>
          <TableCell v-if="store.canManageProject" class="text-right">
            <Button variant="ghost" size="sm" :aria-label="`Remove ${m.user.display_name}`" @click="askRemove(m)">
              Remove
            </Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>

    <section class="max-w-xl rounded-lg border p-4">
      <h3 class="font-medium">Leave project</h3>
      <p class="mt-1 text-sm text-muted-foreground">
        You will lose access to this project. A project always keeps at least one owner.
      </p>
      <Button class="mt-3" variant="outline" data-testid="leave-project" @click="leaveOpen = true">Leave project</Button>
    </section>

    <ConfirmDialog
      v-model:open="removeOpen"
      :title="removeTarget && isMe(removeTarget) ? 'Leave project?' : `Remove ${removeTarget?.user.display_name ?? 'member'}?`"
      description="Their assigned tickets will be unassigned."
      confirm-label="Remove"
      destructive
      :pending="removePending"
      @confirm="doRemove"
    />
    <ConfirmDialog
      v-model:open="leaveOpen"
      title="Leave this project?"
      description="You will lose access until an owner adds you again."
      confirm-label="Leave project"
      destructive
      :pending="leavePending"
      @confirm="doLeave"
    />
  </div>
</template>
