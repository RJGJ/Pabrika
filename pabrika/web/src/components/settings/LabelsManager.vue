<script setup lang="ts">
import { ref } from 'vue'
import type { Label } from '@/api/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { labelClasses, labelSwatchClass } from '@/lib/colors'
import { mapFormError } from '@/lib/forms'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { useProjectSettingsStore } from '@/stores/projectSettings'
import ColorPicker from './ColorPicker.vue'

const store = useProjectSettingsStore()

const newName = ref('')
const newColor = ref('gray')
const createErrors = ref<Record<string, string>>({})
const createFormError = ref<string | null>(null)
const creating = ref(false)

const editingId = ref<string | null>(null)
const editName = ref('')
const editColor = ref('gray')
const editErrors = ref<Record<string, string>>({})
const editFormError = ref<string | null>(null)
const saving = ref(false)

const deleteTarget = ref<Label | null>(null)
const deleteOpen = ref(false)
const deletePending = ref(false)

function validName(n: string): string | null {
  const t = n.trim()
  return t.length < 1 || t.length > 50 ? 'Name must be 1 to 50 characters' : null
}

async function create() {
  createFormError.value = null
  const err = validName(newName.value)
  createErrors.value = err ? { name: err } : {}
  if (err) return
  creating.value = true
  try {
    await store.createLabel(newName.value, newColor.value)
    newName.value = ''
  } catch (e) {
    const m = mapFormError(e, { label_exists: 'name' })
    createErrors.value = m.fields
    createFormError.value = m.form
  } finally {
    creating.value = false
  }
}

function startEdit(l: Label) {
  editingId.value = l.id
  editName.value = l.name
  editColor.value = l.color
  editErrors.value = {}
  editFormError.value = null
}

async function saveEdit() {
  const id = editingId.value
  if (!id) return
  editFormError.value = null
  const err = validName(editName.value)
  editErrors.value = err ? { name: err } : {}
  if (err) return
  saving.value = true
  try {
    await store.updateLabel(id, { name: editName.value.trim(), color: editColor.value })
    editingId.value = null
  } catch (e) {
    const m = mapFormError(e, { label_exists: 'name' })
    editErrors.value = m.fields
    editFormError.value = m.form
  } finally {
    saving.value = false
  }
}

function askDelete(l: Label) {
  deleteTarget.value = l
  deleteOpen.value = true
}

async function doDelete() {
  const l = deleteTarget.value
  if (!l) return
  deletePending.value = true
  try {
    await store.deleteLabel(l.id)
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not delete the label')
  } finally {
    deletePending.value = false
    deleteOpen.value = false
  }
}
</script>

<template>
  <div class="grid max-w-2xl gap-6" data-testid="labels">
    <p v-if="!store.canManageLabels" class="text-sm text-muted-foreground" data-testid="labels-readonly">
      {{
        store.isArchived
          ? 'This project is archived, so labels are read-only.'
          : 'Only editors and owners can manage labels.'
      }}
    </p>

    <form v-if="store.canManageLabels" class="grid gap-3" novalidate data-testid="create-label-form" @submit.prevent="create">
      <FormField id="label-name" label="New label" :error="createErrors.name">
        <Input id="label-name" v-model="newName" maxlength="50" autocomplete="off" />
      </FormField>
      <ColorPicker v-model="newColor" label="New label color" />
      <p v-if="createFormError" role="alert" class="text-sm text-destructive">{{ createFormError }}</p>
      <div>
        <Button type="submit" :disabled="creating">Create label</Button>
      </div>
    </form>

    <EmptyState v-if="store.labels.length === 0" title="No labels yet" description="Labels help you categorize tickets." />
    <ul v-else class="divide-y rounded-lg border">
      <li v-for="l in store.labels" :key="l.id" class="p-3" :data-testid="`label-${l.id}`">
        <form v-if="editingId === l.id" class="grid gap-3" novalidate @submit.prevent="saveEdit">
          <FormField :id="`label-edit-${l.id}`" label="Name" :error="editErrors.name">
            <Input :id="`label-edit-${l.id}`" v-model="editName" maxlength="50" autocomplete="off" />
          </FormField>
          <ColorPicker v-model="editColor" label="Label color" />
          <p v-if="editFormError" role="alert" class="text-sm text-destructive">{{ editFormError }}</p>
          <div class="flex gap-2">
            <Button type="submit" size="sm" :disabled="saving">Save</Button>
            <Button type="button" size="sm" variant="outline" @click="editingId = null">Cancel</Button>
          </div>
        </form>
        <div v-else class="flex items-center gap-3">
          <span class="size-3 rounded-full" :class="labelSwatchClass(l.color)" aria-hidden="true" />
          <span :class="cn('rounded-md px-2 py-0.5 text-sm', labelClasses(l.color))">{{ l.name }}</span>
          <span class="sr-only">{{ l.color }}</span>
          <div v-if="store.canManageLabels" class="ml-auto flex gap-1">
            <Button size="sm" variant="ghost" :aria-label="`Edit ${l.name}`" @click="startEdit(l)">Edit</Button>
            <Button size="sm" variant="ghost" :aria-label="`Delete ${l.name}`" @click="askDelete(l)">Delete</Button>
          </div>
        </div>
      </li>
    </ul>

    <ConfirmDialog
      v-model:open="deleteOpen"
      :title="`Delete label ${deleteTarget?.name ?? ''}?`"
      description="The label will also be removed from every ticket that uses it."
      confirm-label="Delete"
      destructive
      :pending="deletePending"
      @confirm="doDelete"
    />
  </div>
</template>
