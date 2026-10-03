<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Scope } from '@/api/types'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/settings/NativeSelect.vue'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { mapFormError } from '@/lib/forms'
import { useProjectsStore } from '@/stores/projects'
import { useTokensStore } from '@/stores/tokens'

const open = defineModel<boolean>('open', { required: true })
const tokens = useTokensStore()
const projects = useProjectsStore()

const ALL = ''
const name = ref('')
const scope = ref<Scope>('read') // read is the default
const projectId = ref(ALL)
const errors = ref<Record<string, string>>({})
const formError = ref<string | null>(null)
const pending = ref(false)

const scopeOptions = [
  { value: 'read', label: 'Read (view only)' },
  { value: 'write', label: 'Write (create and edit)' },
]
const projectOptions = computed(() => [
  { value: ALL, label: 'All my projects' },
  ...projects.list.map((p) => ({ value: p.id, label: `${p.name} (${p.key})` })),
])

watch(open, (v) => {
  if (!v) return
  name.value = ''
  scope.value = 'read'
  projectId.value = ALL
  errors.value = {}
  formError.value = null
  if (!projects.loaded && !projects.loading) void projects.fetch()
})

async function submit() {
  formError.value = null
  const n = name.value.trim()
  if (n.length < 1 || n.length > 100) {
    errors.value = { name: 'Name must be 1 to 100 characters' }
    return
  }
  errors.value = {}
  pending.value = true
  try {
    // The project limit is sent as the project's ULID, never its key.
    await tokens.create({ name: n, scope: scope.value, ...(projectId.value ? { project_id: projectId.value } : {}) })
    open.value = false // the store now holds the secret; TokenSecretDialog shows it
  } catch (e) {
    const m = mapFormError(e)
    errors.value = m.fields
    formError.value = m.form
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <form class="grid gap-4" novalidate data-testid="create-token-form" @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>Create API token</DialogTitle>
          <DialogDescription>Tokens let agents use Pabrika over MCP or the REST API.</DialogDescription>
        </DialogHeader>
        <FormField id="token-name" label="Name" :error="errors.name">
          <Input id="token-name" v-model="name" maxlength="100" autocomplete="off" placeholder="e.g. Claude Code" />
        </FormField>
        <FormField id="token-scope" label="Scope" :error="errors.scope">
          <NativeSelect id="token-scope" v-model="scope" :options="scopeOptions" />
        </FormField>
        <FormField id="token-project" label="Project limit" :error="errors.project_id">
          <NativeSelect id="token-project" v-model="projectId" :options="projectOptions" />
        </FormField>
        <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
        <DialogFooter>
          <Button type="button" variant="outline" :disabled="pending" @click="open = false">Cancel</Button>
          <Button type="submit" :disabled="pending">Create token</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
