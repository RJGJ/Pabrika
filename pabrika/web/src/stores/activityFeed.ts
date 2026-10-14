import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { activityFeed, type FeedQuery } from '@/api/activity'
import { ApiError } from '@/api/client'
import { members as membersApi } from '@/api/members'
import type { ActivityFeedItem, ApiEvent, Member } from '@/api/types'
import { emptyFeedFilters, matchesFeedFilters, type FeedFilters } from '@/lib/feedFilters'

export type FeedLoadState = 'idle' | 'loading' | 'ready' | 'error' | 'no-access' | 'not-found'

/** Events that add a row to the feed. */
const FEED_EVENTS = new Set<string>([
  'ticket.created', 'ticket.updated', 'ticket.moved', 'ticket.deleted', 'comment.added', 'decision.recorded',
])

const sameKey = (a: string | null, b: string): boolean => !!a && a.toLowerCase() === b.toLowerCase()

/** State for `/p/:key/activity`: the first page, load more, filters and the "N new events" counter. */
export const useActivityFeedStore = defineStore('activityFeed', () => {
  const key = ref<string | null>(null)
  const items = ref<ActivityFeedItem[]>([])
  const nextCursor = ref<string | null>(null)
  const members = ref<Member[]>([])
  const loadState = ref<FeedLoadState>('idle')
  const loadingMore = ref(false)
  const pending = ref(0)
  const live = ref<'idle' | 'live' | 'reconnecting'>('idle')
  const filters = ref<FeedFilters>(emptyFeedFilters())

  let seq = 0
  let opened = false

  const hasMore = computed(() => !!nextCursor.value)
  const visible = computed(() => items.value.filter((i) => matchesFeedFilters(i, filters.value)))

  const query = (cursor: string | null): FeedQuery => ({
    cursor,
    actor_type: filters.value.actor === 'agent' ? 'api_token' : filters.value.actor === 'human' ? 'user' : undefined,
  })

  function fail(e: unknown, quiet: boolean): void {
    if (e instanceof ApiError && e.status === 404) {
      loadState.value = quiet ? 'no-access' : 'not-found'
      return
    }
    if (!quiet) loadState.value = 'error'
  }

  async function fetchFirst(k: string, quiet: boolean): Promise<void> {
    const mine = ++seq
    if (!quiet) loadState.value = 'loading'
    try {
      const [page, mem] = await Promise.all([
        activityFeed.list(k, query(null)),
        quiet && members.value.length ? Promise.resolve(members.value) : membersApi.list(k).catch(() => members.value),
      ])
      if (mine !== seq) return
      items.value = page.items
      nextCursor.value = page.next_cursor
      members.value = mem
      pending.value = 0
      loadState.value = 'ready'
    } catch (e) {
      if (mine === seq) fail(e, quiet)
    }
  }

  /** Load the feed for a project. Coming back to the same project keeps the list and refreshes it quietly. */
  async function load(k: string): Promise<void> {
    if (sameKey(key.value, k) && loadState.value === 'ready') return fetchFirst(k, true)
    reset()
    key.value = k
    return fetchFirst(k, false)
  }

  async function loadMore(): Promise<void> {
    if (!key.value || !nextCursor.value || loadingMore.value) return
    const mine = seq
    loadingMore.value = true
    try {
      const page = await activityFeed.list(key.value, query(nextCursor.value))
      if (mine !== seq) return
      const seen = new Set(items.value.map((i) => i.id))
      items.value = [...items.value, ...page.items.filter((i) => !seen.has(i.id))]
      nextCursor.value = page.next_cursor
    } catch {
      /* the button stays; the user can retry */
    } finally {
      loadingMore.value = false
    }
  }

  /** The "N new events, show" bar: reload from the top. */
  async function showNew(): Promise<void> {
    if (key.value) await fetchFirst(key.value, true)
  }

  function noteEvent(e: ApiEvent): void {
    if (loadState.value === 'ready' && FEED_EVENTS.has(e.type)) pending.value++
  }

  /** Every stream open; the first one needs nothing, a reconnect looks for rows missed meanwhile. */
  async function onStreamOpen(): Promise<void> {
    if (!opened) {
      opened = true
      return
    }
    if (!key.value || loadState.value !== 'ready') return
    try {
      const top = items.value[0]?.id
      const page = await activityFeed.list(key.value, { ...query(null), limit: 1 })
      if (page.items[0] && page.items[0].id !== top) pending.value = Math.max(pending.value, 1)
    } catch {
      /* ignore */
    }
  }

  /** Server-side narrowing (actor type) refetches; the rest is applied to the loaded rows. */
  async function setFilters(f: FeedFilters): Promise<void> {
    const refetch = f.actor !== filters.value.actor
    filters.value = f
    if (refetch && key.value && loadState.value === 'ready') await fetchFirst(key.value, true)
  }

  function handleLostAccess(): void {
    loadState.value = 'no-access'
  }

  function reset(): void {
    seq++
    key.value = null
    items.value = []
    nextCursor.value = null
    members.value = []
    loadState.value = 'idle'
    loadingMore.value = false
    pending.value = 0
    live.value = 'idle'
    opened = false
  }

  return {
    key, items, nextCursor, members, loadState, loadingMore, pending, live, filters, hasMore, visible,
    load, loadMore, showNew, noteEvent, onStreamOpen, setFilters, handleLostAccess, reset,
  }
})
