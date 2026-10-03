<script setup lang="ts">
import { ref, watch } from 'vue'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { mapFormError } from '@/lib/forms'
import { isValidKey, normalizeKey, suggestKey } from '@/lib/projectKey'
import { useProjectsStore } from '@/stores/projects'

const open = defineModel<boolean>('open', { required: true })
const projects = useProjectsStore()

const name = ref('')
const key = ref('')
const keyEdited = ref(false)
const description = ref('')
const errors = ref<Record<string, string>>({})
const formError = ref<string | null>(null)
const pending = ref(false)

watch(open, (v) => {
  if (v) {
    name.value = key.value = description.value = ''
    keyEdited.value = false
    errors.value = {}
    formError.value = null
  }
})

function onName(v: string | number) {
  name.value = String(v)
  if (!keyEdited.value) key.value = suggestKey(name.value)
}
function onKey(v: string | number) {
  keyEdited.value = true
  key.value = normalizeKey(String(v))
}

function validate(): boolean {
  const e: Record<string, string> = {}
  const n = name.value.trim()
  if (n.length < 1 || n.length > 100) e.name = 'Name must be 1 to 100 characters'
  if (!isValidKey(key.value)) e.key = 'Key must be 2 to 6 letters'
  if (description.value.length > 2000) e.description = 'Description must be at most 2,000 characters'
  errors.value = e
  return Object.keys(e).length === 0
}

async function submit() {
  formError.value = null
  if (!validate()) return
  pending.value = true
  try {
    await projects.create({ key: key.value, name: name.value.trim(), description: description.value.trim() })
    open.value = false
  } catch (e) {
    const m = mapFormError(e, { key_taken: 'key' })
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
      <form class="grid gap-4" novalidate @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>New project</DialogTitle>
          <DialogDescription>Tickets are numbered with the key, for example WEB-1.</DialogDescription>
        </DialogHeader>
        <FormField id="project-name" label="Name" :error="errors.name">
          <Input id="project-name" :model-value="name" maxlength="100" autocomplete="off" @update:model-value="onName" />
        </FormField>
        <FormField id="project-key" label="Key" hint="2 to 6 letters" :error="errors.key">
          <Input
            id="project-key"
            :model-value="key"
            maxlength="6"
            autocomplete="off"
            class="font-mono uppercase"
            @update:model-value="onKey"
          />
        </FormField>
        <FormField id="project-description" label="Description (optional)" :error="errors.description">
          <Textarea id="project-description" v-model="description" rows="3" maxlength="2000" />
        </FormField>
        <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
        <DialogFooter>
          <Button type="button" variant="outline" :disabled="pending" @click="open = false">Cancel</Button>
          <Button type="submit" :disabled="pending">Create project</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
