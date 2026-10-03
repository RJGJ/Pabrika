<script setup lang="ts">
import { ref, watch } from 'vue'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { mapFormError } from '@/lib/forms'
import { notify } from '@/lib/toast'
import { useProjectSettingsStore } from '@/stores/projectSettings'
import DangerZone from './DangerZone.vue'

const store = useProjectSettingsStore()

const name = ref('')
const description = ref('')
const errors = ref<Record<string, string>>({})
const formError = ref<string | null>(null)
const pending = ref(false)
const archivePending = ref(false)

let base = { name: '', description: '' }

function reset() {
  base = { name: store.project?.name ?? '', description: store.project?.description ?? '' }
  name.value = base.name
  description.value = base.description
  errors.value = {}
  formError.value = null
}

watch(
  () => store.project,
  (p) => {
    if (!p) return
    // A live refresh replaces the fields only when the user has no unsaved edit.
    const untouched = name.value === base.name && description.value === base.description
    if (untouched) reset()
  },
  { immediate: true },
)

function validate(): boolean {
  const e: Record<string, string> = {}
  const n = name.value.trim()
  if (n.length < 1 || n.length > 100) e.name = 'Name must be 1 to 100 characters'
  if (description.value.length > 2000) e.description = 'Description must be at most 2,000 characters'
  errors.value = e
  return Object.keys(e).length === 0
}

async function save() {
  formError.value = null
  if (!validate()) return
  pending.value = true
  try {
    await store.updateProject({ name: name.value.trim(), description: description.value.trim() })
    reset()
    notify('success', 'Project updated')
  } catch (e) {
    const m = mapFormError(e)
    errors.value = m.fields
    formError.value = m.form
  } finally {
    pending.value = false
  }
}

async function toggleArchived(v: boolean) {
  archivePending.value = true
  try {
    await store.updateProject({ archived: v })
    notify('success', v ? 'Project archived' : 'Project unarchived')
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not update the project')
  } finally {
    archivePending.value = false
  }
}
</script>

<template>
  <div v-if="store.project" class="grid gap-8" data-testid="general-settings">
    <form v-if="store.canManageProject" class="grid max-w-xl gap-4" novalidate @submit.prevent="save">
      <FormField id="settings-key" label="Key" hint="The key cannot be changed.">
        <Input id="settings-key" :model-value="store.project.key" readonly class="font-mono" />
      </FormField>
      <FormField id="settings-name" label="Name" :error="errors.name">
        <Input id="settings-name" v-model="name" maxlength="100" autocomplete="off" />
      </FormField>
      <FormField id="settings-description" label="Description" :error="errors.description">
        <Textarea id="settings-description" v-model="description" rows="4" maxlength="2000" />
      </FormField>
      <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
      <div>
        <Button type="submit" :disabled="pending">Save changes</Button>
      </div>
    </form>

    <dl v-else class="grid max-w-xl gap-4 text-sm" data-testid="general-readonly">
      <div>
        <dt class="text-muted-foreground">Key</dt>
        <dd class="font-mono">{{ store.project.key }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Name</dt>
        <dd>{{ store.project.name }}</dd>
      </div>
      <div>
        <dt class="text-muted-foreground">Description</dt>
        <dd class="whitespace-pre-wrap">{{ store.project.description || 'No description' }}</dd>
      </div>
      <p class="text-muted-foreground">Only project owners can change these settings.</p>
    </dl>

    <section v-if="store.canManageProject" class="max-w-xl rounded-lg border p-4">
      <div class="flex items-center justify-between gap-4">
        <div>
          <h3 class="font-medium"><label for="settings-archived">Archive project</label></h3>
          <p class="text-sm text-muted-foreground">
            Archived projects are read-only. You can unarchive them at any time.
          </p>
        </div>
        <Switch
          id="settings-archived"
          :model-value="store.isArchived"
          :disabled="archivePending"
          @update:model-value="toggleArchived"
        />
      </div>
    </section>

    <DangerZone v-if="store.canManageProject" />
  </div>
</template>
