<script setup lang="ts">
import { computed, ref } from 'vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { notify } from '@/lib/toast'
import { useTokensStore } from '@/stores/tokens'

// Shown whenever the tokens store holds a freshly created secret. Closing the dialog (any way)
// clears it from memory. It is never written to storage, the URL or logs.
const tokens = useTokensStore()
const open = computed({
  get: () => tokens.secret !== null,
  set: (v: boolean) => {
    if (!v) tokens.clearSecret()
  },
})
const copied = ref(false)

async function copy() {
  const value = tokens.secret?.value
  if (!value) return
  try {
    await navigator.clipboard.writeText(value)
    copied.value = true
    notify('success', 'Token copied')
  } catch {
    notify('error', 'Could not copy, select the token and copy it manually')
  }
}

function useInSnippet() {
  tokens.useSecretInSnippet()
  open.value = false
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent data-testid="token-secret-dialog">
      <DialogHeader>
        <DialogTitle>Token created</DialogTitle>
        <DialogDescription>Copy the token for "{{ tokens.secret?.tokenName }}" now.</DialogDescription>
      </DialogHeader>
      <Alert>
        <AlertDescription>You won't be able to see this again.</AlertDescription>
      </Alert>
      <div class="flex gap-2">
        <Input readonly :model-value="tokens.secret?.value ?? ''" class="font-mono text-xs" aria-label="Token secret" data-testid="token-secret" @focus="($event.target as HTMLInputElement).select()" />
        <Button type="button" variant="outline" data-testid="copy-secret" @click="copy">{{ copied ? 'Copied' : 'Copy' }}</Button>
      </div>
      <DialogFooter>
        <Button type="button" variant="outline" data-testid="use-in-snippet" @click="useInSnippet">Use in MCP snippet</Button>
        <Button type="button" data-testid="close-secret" @click="open = false">Done</Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
