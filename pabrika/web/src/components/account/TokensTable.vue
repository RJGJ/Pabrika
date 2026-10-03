<script setup lang="ts">
import { ref } from 'vue'
import type { Token } from '@/api/types'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import EmptyState from '@/components/common/EmptyState.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { relativeTime } from '@/lib/dates'
import { mapFormError } from '@/lib/forms'
import { notify } from '@/lib/toast'
import { cn } from '@/lib/utils'
import { useTokensStore } from '@/stores/tokens'

const store = useTokensStore()
const target = ref<Token | null>(null)
const open = ref(false)
const pending = ref(false)

function ask(t: Token) {
  target.value = t
  open.value = true
}

async function doRevoke() {
  const t = target.value
  if (!t) return
  pending.value = true
  try {
    await store.revoke(t.id)
    notify('success', 'Token revoked')
  } catch (e) {
    notify('error', mapFormError(e).form ?? 'Could not revoke the token')
  } finally {
    pending.value = false
    open.value = false
  }
}

const date = (iso: string) => new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
</script>

<template>
  <div data-testid="tokens-table">
    <div v-if="store.loading && store.items.length === 0" class="grid gap-2">
      <Skeleton class="h-10 w-full" />
      <Skeleton class="h-10 w-full" />
    </div>
    <div v-else-if="store.error" role="alert" class="flex items-center gap-3 text-sm text-destructive">
      {{ store.error }}
      <Button size="sm" variant="outline" @click="store.load()">Retry</Button>
    </div>
    <EmptyState v-else-if="store.items.length === 0" title="No API tokens" description="Create a token to connect an agent over MCP." />
    <Table v-else>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Token</TableHead>
          <TableHead>Scope</TableHead>
          <TableHead>Project</TableHead>
          <TableHead>Last used</TableHead>
          <TableHead>Created</TableHead>
          <TableHead>Status</TableHead>
          <TableHead><span class="sr-only">Actions</span></TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        <TableRow
          v-for="t in store.items"
          :key="t.id"
          :data-testid="`token-${t.id}`"
          :data-revoked="t.revoked_at ? 'true' : 'false'"
          :class="cn(t.revoked_at && 'text-muted-foreground opacity-60')"
        >
          <TableCell class="font-medium">{{ t.name }}</TableCell>
          <TableCell class="font-mono text-xs">{{ t.token_prefix }}...</TableCell>
          <TableCell><Badge variant="outline">{{ t.scope }}</Badge></TableCell>
          <TableCell>{{ t.project ? t.project.key : 'All projects' }}</TableCell>
          <TableCell>{{ t.last_used_at ? relativeTime(t.last_used_at) : 'Never' }}</TableCell>
          <TableCell>{{ date(t.created_at) }}</TableCell>
          <TableCell>
            <Badge v-if="t.revoked_at" variant="secondary">Revoked</Badge>
            <span v-else>Active</span>
          </TableCell>
          <TableCell class="text-right">
            <Button v-if="!t.revoked_at" size="sm" variant="ghost" :aria-label="`Revoke ${t.name}`" @click="ask(t)">
              Revoke
            </Button>
          </TableCell>
        </TableRow>
      </TableBody>
    </Table>

    <ConfirmDialog
      v-model:open="open"
      :title="`Revoke ${target?.name ?? 'token'}?`"
      description="Anything using this token will stop working immediately."
      confirm-label="Revoke"
      destructive
      :pending="pending"
      @confirm="doRevoke"
    />
  </div>
</template>
