<script setup lang="ts">
import { ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
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
const password = ref('')
const pending = ref(false)
const formError = ref<string | null>(null)
const errors = ref<Record<string, string>>({})

async function submit() {
  formError.value = null
  errors.value = {}
  pending.value = true
  try {
    await auth.login(email.value.trim(), password.value)
    await router.replace(safeRedirect(route.query.redirect))
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
  <AuthCard title="Sign in" description="Welcome back.">
    <form class="grid gap-4" novalidate @submit.prevent="submit">
      <FormField id="login-email" label="Email" :error="errors.email">
        <Input id="login-email" v-model="email" type="email" autocomplete="username" required />
      </FormField>
      <FormField id="login-password" label="Password" :error="errors.password">
        <Input id="login-password" v-model="password" type="password" autocomplete="current-password" required />
      </FormField>
      <p v-if="formError" role="alert" class="text-sm text-destructive">{{ formError }}</p>
      <Button type="submit" :disabled="pending">Sign in</Button>
      <p v-if="auth.signupEnabled" class="text-center text-sm text-muted-foreground">
        No account?
        <RouterLink :to="{ name: 'signup', query: route.query.redirect ? { redirect: route.query.redirect } : undefined }" class="underline">
          Create account
        </RouterLink>
      </p>
    </form>
  </AuthCard>
</template>
