<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import type { ActivityFeedItem, Member } from '@/api/types'
import BotBadge from '@/components/common/BotBadge.vue'
import UserAvatar from '@/components/common/UserAvatar.vue'
import { humanizeActivity } from '@/lib/activity'
import { toneClasses } from '@/lib/colors'
import { relativeTime } from '@/lib/dates'
import { parseRoleTag } from '@/lib/roleTag'
import { cn } from '@/lib/utils'

const props = defineProps<{ item: ActivityFeedItem; projectKey: string; members: Member[] }>()

const tag = computed(() => (props.item.kind === 'comment' ? parseRoleTag(props.item.summary ?? '', props.item.actor) : null))
/** Comment text without an exact first tag line. */
const commentText = computed(() => (tag.value ? tag.value.text : (props.item.summary ?? '')))
const text = computed(() =>
  props.item.kind === 'comment'
    ? 'commented'
    : humanizeActivity(
        { action: props.item.action ?? (props.item.kind === 'decision' ? 'decision' : 'updated'), changes: props.item.changes ?? {}, summary: props.item.summary },
        props.members,
      ),
)
const number = computed(() => props.item.ticket.ref.slice(props.item.ticket.ref.lastIndexOf('-') + 1))
</script>

<template>
  <li :data-feed-id="item.id" :data-kind="item.kind" class="grid gap-1 border-b px-1 py-3 text-sm">
    <div class="flex flex-wrap items-center gap-x-2 gap-y-1">
      <UserAvatar :name="item.actor.name" />
      <span class="font-medium">{{ item.actor.name }}</span>
      <BotBadge v-if="item.actor.bot" :role="tag?.roleLabel" />
      <span
        v-if="tag"
        data-testid="type-chip"
        :class="cn('rounded-full px-2 text-[11px] font-medium', toneClasses(tag.tone))"
      >{{ tag.typeLabel }}</span>
      <span>{{ text }}</span>
      <RouterLink
        :to="{ name: 'ticket', params: { key: projectKey, number } }"
        class="font-mono text-xs underline underline-offset-2"
        data-testid="ticket-link"
      >{{ item.ticket.ref }}</RouterLink>
      <span class="truncate text-muted-foreground">{{ item.ticket.title }}</span>
      <time class="ml-auto text-xs text-muted-foreground" :datetime="item.created_at" :title="item.created_at">
        {{ relativeTime(item.created_at) }}
      </time>
    </div>
    <p v-if="item.kind === 'comment' && commentText" class="line-clamp-3 pl-8 text-muted-foreground break-words whitespace-pre-line">{{ commentText }}</p>
  </li>
</template>
