<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { mapFormError } from '@/lib/forms'
import { useProjectSettingsStore } from '@/stores/projectSettings'

const store = useProjectSettingsStore()
const open = ref(false)
const typed = ref('')
const pending = ref(false)
const error = ref<string | null>(null)

const matches = computed(() => !!store.project && typed.value.trim() === store.project.key)

watch(open, (v) => {
  if (v) {
    typed.value = ''
    error.value = null
  }
})

async function confirm() {
  if (!matches.value) return
  pending.value = true
  error.value = null
  try {
    await store.deleteProject() // resets the store and navigates home on success
    open.value = false
  } catch (e) {
    error.value = mapFormError(e).form ?? 'Could not delete the project'
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <section v-if="store.project" class="max-w-xl rounded-lg border border-destructive/50 p-4" data-testid="danger-zone">
    <h3 class="font-medium text-destructive">Danger zone</h3>
    <p class="mt-1 text-sm text-muted-foreground">
      Deleting a project permanently removes its tickets, labels and members. This cannot be undone.
    </p>
    <Button class="mt-3" variant="destructive" data-testid="delete-project" @click="open = true">Delete project</Button>

    <Dialog v-model:open="open">
      <DialogContent>
        <form class="grid gap-4" novalidate @submit.prevent="confirm">
          <DialogHeader>
            <DialogTitle>Delete {{ store.project.name }}?</DialogTitle>
            <DialogDescription>
              Type the project key <strong class="font-mono">{{ store.project.key }}</strong> to confirm.
            </DialogDescription>
          </DialogHeader>
          <FormField id="delete-confirm-key" label="Project key">
            <Input id="delete-confirm-key" v-model="typed" autocomplete="off" class="font-mono" />
          </FormField>
          <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>
          <DialogFooter>
            <Button type="button" variant="outline" :disabled="pending" @click="open = false">Cancel</Button>
            <Button type="submit" variant="destructive" :disabled="!matches || pending" data-testid="confirm-delete">
              Delete project
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  </section>
</template>
