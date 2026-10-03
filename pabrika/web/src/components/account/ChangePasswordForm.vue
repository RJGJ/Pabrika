<script setup lang="ts">
import { ref } from 'vue'
import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { mapFormError } from '@/lib/forms'
import { notify } from '@/lib/toast'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const current = ref('')
const next = ref('')
const errors = ref<Record<string, string>>({})
const formError = ref<string | null>(null)
const pending = ref(false)

async function submit() {
  formError.value = null
  const e: Record<string, string> = {}
  if (!current.value) e.current_password = 'Enter your current password'
  if (next.value.length < 10) e.new_password = 'Password must be at least 10 characters'
  errors.value = e
  if (Object.keys(e).length > 0) return
  pending.value = true
  try {
    // A wrong current password is a 422 field error (never 401), so it can not trigger a login redirect.
    await auth.changePassword(current.value, next.value)
    current.value = ''
    next.value = ''
    notify('success', 'Password changed. Other sessions were signed out.')
  } catch (err) {
    const m = mapFormError(err)
    errors.value = m.fields
    formError.value = m.form
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Change password</CardTitle>
      <CardDescription>Changing your password signs you out everywhere else.</CardDescription>
    </CardHeader>
    <CardContent>
      <form class="grid max-w-md gap-4" novalidate data-testid="password-form" @submit.prevent="submit">
        <FormField id="current-password" label="Current password" :error="errors.current_password">
          <Input id="current-password" v-model="current" type="password" autocomplete="current-password" />
        </FormField>
        <FormField id="new-password" label="New password" hint="At least 10 characters" :error="errors.new_password">
          <Input id="new-password" v-model="next" type="password" autocomplete="new-password" />
        </FormField>
        <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
        <div>
          <Button type="submit" :disabled="pending">Change password</Button>
        </div>
      </form>
    </CardContent>
  </Card>
</template>
