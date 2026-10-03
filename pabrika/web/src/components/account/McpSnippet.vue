<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { notify } from '@/lib/toast'
import { useTokensStore } from '@/stores/tokens'

const tokens = useTokensStore()

const origin = window.location.origin
const command = computed(
  () =>
    `claude mcp add --transport http pabrika ${origin}/mcp --header "Authorization: Bearer ${tokens.snippetSecret ?? 'pb_your_token_here'}"`,
)

async function copy() {
  try {
    await navigator.clipboard.writeText(command.value)
    notify('success', 'Command copied')
  } catch {
    notify('error', 'Could not copy, select the command and copy it manually')
  }
}
</script>

<template>
  <Card>
    <CardHeader>
      <CardTitle>Connect an agent (MCP)</CardTitle>
      <CardDescription>Run this in a terminal to add Pabrika to Claude Code.</CardDescription>
    </CardHeader>
    <CardContent class="grid gap-3">
      <pre
        class="overflow-x-auto rounded-md bg-muted p-3 text-xs"
        data-testid="mcp-command"
      ><code>{{ command }}</code></pre>
      <div class="flex flex-wrap items-center gap-2">
        <Button variant="outline" size="sm" data-testid="copy-command" @click="copy">Copy command</Button>
        <Button v-if="tokens.snippetSecret" variant="ghost" size="sm" data-testid="clear-snippet-token" @click="tokens.snippetSecret = null">
          Remove token from snippet
        </Button>
      </div>
      <p class="text-sm text-muted-foreground">
        Replace <code>pb_your_token_here</code> with a token from the list above. When developing, use the Go
        server's address (for example <code>http://localhost:8080</code>) instead of the Vite address.
      </p>
      <p class="text-sm text-muted-foreground">
        Other MCP clients: connect to <code>{{ origin }}/mcp</code> with the Streamable HTTP transport and send an
        <code>Authorization: Bearer &lt;token&gt;</code> header.
      </p>
    </CardContent>
  </Card>
</template>
