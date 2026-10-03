<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import ChangePasswordForm from '@/components/account/ChangePasswordForm.vue'
import CreateTokenDialog from '@/components/account/CreateTokenDialog.vue'
import McpSnippet from '@/components/account/McpSnippet.vue'
import ProfileForm from '@/components/account/ProfileForm.vue'
import TokenSecretDialog from '@/components/account/TokenSecretDialog.vue'
import TokensTable from '@/components/account/TokensTable.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useTokensStore } from '@/stores/tokens'

const tokens = useTokensStore()
const createOpen = ref(false)

onMounted(() => void tokens.load())
// Secrets are in memory only: leaving the screen drops them.
onBeforeUnmount(() => tokens.reset())
</script>

<template>
  <div class="mx-auto grid max-w-4xl gap-6 p-4 md:p-6">
    <h1 class="text-xl font-semibold">Account settings</h1>
    <ProfileForm />
    <ChangePasswordForm />
    <Card>
      <CardHeader class="flex-row items-start justify-between gap-4">
        <div class="grid gap-1.5">
          <CardTitle>API tokens</CardTitle>
          <CardDescription>Tokens give agents access to your projects. They never see more than you do.</CardDescription>
        </div>
        <Button data-testid="new-token" @click="createOpen = true">New token</Button>
      </CardHeader>
      <CardContent><TokensTable /></CardContent>
    </Card>
    <McpSnippet />
    <CreateTokenDialog v-model:open="createOpen" />
    <TokenSecretDialog />
  </div>
</template>
