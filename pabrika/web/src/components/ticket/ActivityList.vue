<script setup lang="ts">
import type { Activity, Member } from '@/api/types'
import BotBadge from '@/components/common/BotBadge.vue'
import { Button } from '@/components/ui/button'
import { humanizeActivity } from '@/lib/activity'
import { relativeTime } from '@/lib/dates'

defineProps<{ items: Activity[]; members: Member[]; hasMore: boolean; loadingMore?: boolean }>()
const emit = defineEmits<{ loadMore: [] }>()
</script>

<template>
  <div>
    <p v-if="items.length === 0" class="text-sm text-muted-foreground">No activity yet.</p>
    <ul v-else class="grid gap-2" aria-label="Activity">
      <li v-for="a in items" :key="a.id" class="text-sm">
        <span class="font-medium">{{ a.actor.name }}</span>
        <BotBadge v-if="a.actor.bot" class="mx-1" />
        {{ ' ' }}{{ humanizeActivity(a, members) }}
        <time class="ml-1 text-xs text-muted-foreground" :datetime="a.created_at" :title="a.created_at">
          {{ relativeTime(a.created_at) }}
        </time>
      </li>
    </ul>
    <Button v-if="hasMore" variant="outline" size="sm" class="mt-3" :disabled="loadingMore" @click="emit('loadMore')">
      Load more
    </Button>
  </div>
</template>
