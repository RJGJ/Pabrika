<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ApiError } from '@/api/client'
import FormField from '@/components/common/FormField.vue'
import AuthCard from '@/components/layout/AuthCard.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { mapFormError } from '@/lib/forms'
import { safeRedirect } from '@/lib/redirect'
import { useAuthStore } from '@/stores/auth'

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const email = ref('')
const displayName = ref('')
const password = ref('')
const pending = ref(false)
const formError = ref<string | null>(null)
const errors = ref<Record<string, string>>({})

function validate(): boolean {
  const e: Record<string, string> = {}
  if (!/^\S+@\S+\.\S+$/.test(email.value.trim())) e.email = 'Enter a valid email address'
  const n = displayName.value.trim()
  if (n.length < 1 || n.length > 100) e.display_name = 'Display name must be 1 to 100 characters'
  if (password.value.length < 10) e.password = 'Password must be at least 10 characters'
  errors.value = e
  return Object.keys(e).length === 0
}

async function submit() {
  formError.value = null
  if (!validate()) return
  pending.value = true
  try {
    await auth.signup(email.value.trim(), displayName.value.trim(), password.value)
    await router.replace(safeRedirect(route.query.redirect))
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) {
      await router.replace({ name: 'login' })
      return
    }
    const m = mapFormError(e, { email_taken: 'email' })
    errors.value = m.fields
    formError.value = m.form
  } finally {
    pending.value = false
  }
}
</script>

<template>
  <AuthCard title="Create account">
    <form class="grid gap-4" novalidate @submit.prevent="submit">
      <FormField id="signup-email" label="Email" :error="errors.email">
        <Input id="signup-email" v-model="email" type="email" autocomplete="username" required />
      </FormField>
      <FormField id="signup-name" label="Display name" :error="errors.display_name">
        <Input id="signup-name" v-model="displayName" maxlength="100" autocomplete="name" required />
      </FormField>
      <FormField id="signup-password" label="Password" hint="At least 10 characters" :error="errors.password">
        <Input id="signup-password" v-model="password" type="password" autocomplete="new-password" required />
      </FormField>
      <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
      <Button type="submit" :disabled="pending">Create account</Button>
      <p class="text-center text-sm text-muted-foreground">
        Already have an account?
        <RouterLink :to="{ name: 'login', query: route.query.redirect ? { redirect: route.query.redirect } : undefined }" class="underline">
          Sign in
        </RouterLink>
      </p>
    </form>
  </AuthCard>
</template>
