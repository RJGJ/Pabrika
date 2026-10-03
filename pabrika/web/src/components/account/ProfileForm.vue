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
const displayName = ref(auth.user?.display_name ?? '')
const errors = ref<Record<string, string>>({})
const formError = ref<string | null>(null)
const pending = ref(false)

async function submit() {
  formError.value = null
  const n = displayName.value.trim()
  if (n.length < 1 || n.length > 100) {
    errors.value = { display_name: 'Display name must be 1 to 100 characters' }
    return
  }
  errors.value = {}
  pending.value = true
  try {
    await auth.updateProfile(n)
    displayName.value = auth.user?.display_name ?? n
    notify('success', 'Profile updated')
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
  <Card>
    <CardHeader>
      <CardTitle>Profile</CardTitle>
      <CardDescription>Your name is shown to other project members.</CardDescription>
    </CardHeader>
    <CardContent>
      <form class="grid max-w-md gap-4" novalidate data-testid="profile-form" @submit.prevent="submit">
        <FormField id="profile-email" label="Email" hint="Email cannot be changed.">
          <Input id="profile-email" :model-value="auth.user?.email ?? ''" readonly />
        </FormField>
        <FormField id="profile-name" label="Display name" :error="errors.display_name">
          <Input id="profile-name" v-model="displayName" maxlength="100" autocomplete="name" />
        </FormField>
        <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
        <div>
          <Button type="submit" :disabled="pending">Save profile</Button>
        </div>
      </form>
    </CardContent>
  </Card>
</template>
