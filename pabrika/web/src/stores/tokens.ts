import { defineStore } from 'pinia'
import { ref } from 'vue'
import { tokens as tokensApi } from '@/api/tokens'
import type { CreateTokenInput, Token } from '@/api/types'

/**
 * API token management for the account screen. Secrets live in memory only: `secret` holds the value
 * shown once in TokenSecretDialog and is cleared when it closes; `snippetSecret` is set only when the
 * user opts to fill the MCP snippet and is cleared when the account screen unmounts (`reset`).
 * Nothing here is ever written to storage, the URL or logs.
 */
export const useTokensStore = defineStore('tokens', () => {
  const items = ref<Token[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const secret = ref<{ value: string; tokenName: string } | null>(null)
  const snippetSecret = ref<string | null>(null)

  async function load(): Promise<void> {
    loading.value = true
    error.value = null
    try {
      items.value = await tokensApi.list()
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Failed to load tokens'
    } finally {
      loading.value = false
    }
  }

  /** Create a token and keep its secret in memory for the one-time dialog. Errors propagate to the form. */
  async function create(input: CreateTokenInput): Promise<Token> {
    const res = await tokensApi.create(input)
    items.value = [res.token, ...items.value]
    secret.value = { value: res.secret, tokenName: res.token.name }
    return res.token
  }

  async function revoke(id: string): Promise<void> {
    await tokensApi.revoke(id)
    const now = new Date().toISOString()
    items.value = items.value.map((t) => (t.id === id ? { ...t, revoked_at: t.revoked_at ?? now } : t))
  }

  function useSecretInSnippet(): void {
    snippetSecret.value = secret.value?.value ?? null
  }

  function clearSecret(): void {
    secret.value = null
  }

  function reset(): void {
    items.value = []
    loading.value = false
    error.value = null
    secret.value = null
    snippetSecret.value = null
  }

  return { items, loading, error, secret, snippetSecret, load, create, revoke, useSecretInSnippet, clearSecret, reset }
})
