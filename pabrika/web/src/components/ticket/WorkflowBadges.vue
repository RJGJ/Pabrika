<script setup lang="ts">
import { computed } from 'vue'
import { BanIcon, HourglassIcon, RotateCcwIcon, UserCheckIcon } from '@lucide/vue'
import type { Round } from '@/api/types'
import { toneClasses } from '@/lib/colors'
import { cn } from '@/lib/utils'
import {
  FLAG_TEXT, roundTone, showRound, stageText, type Classified,
} from '@/lib/workflow'

const props = defineProps<{ classified: Classified<{ name: string }>; round?: Round | null }>()

const stage = computed(() => props.classified.stage)
const isGate = computed(() => !!stage.value && stage.value.startsWith('awaiting-'))
const roundShown = computed(() => showRound(props.classified, props.round))
const ROUND_TONE = { normal: 'neutral', amber: 'amber', red: 'red' } as const
const base = 'inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[11px] font-medium'
</script>

<template>
  <template v-if="classified.hasWorkflow && (stage || classified.flags.length || roundShown)">
    <span
      v-if="stage"
      data-testid="stage-badge"
      :data-stage="stage"
      :class="cn(base, toneClasses(isGate ? 'amber' : 'blue'))"
    >
      <UserCheckIcon v-if="isGate" class="size-3" aria-hidden="true" />
      <HourglassIcon v-else class="size-3" aria-hidden="true" />
      {{ stageText(stage) }}
    </span>
    <span
      v-for="f in classified.flags"
      :key="f"
      :data-testid="`flag-${f}`"
      :class="cn(base, toneClasses(f === 'blocked' ? 'red' : 'orange'))"
    >
      <BanIcon v-if="f === 'blocked'" class="size-3" aria-hidden="true" />
      <RotateCcwIcon v-else class="size-3" aria-hidden="true" />
      {{ FLAG_TEXT[f] }}
    </span>
    <span
      v-if="roundShown && round"
      data-testid="round-badge"
      :data-tone="roundTone(round)"
      :class="cn(base, toneClasses(ROUND_TONE[roundTone(round)]))"
    >Round {{ round.n }}/{{ round.max }}</span>
  </template>
</template>
