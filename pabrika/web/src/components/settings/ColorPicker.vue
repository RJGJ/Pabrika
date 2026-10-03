<script setup lang="ts">
import { LABEL_COLORS } from '@/api/types'
import { labelSwatchClass } from '@/lib/colors'
import { cn } from '@/lib/utils'

defineProps<{ disabled?: boolean; label?: string }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <div role="radiogroup" :aria-label="label ?? 'Color'" class="flex flex-wrap gap-1.5">
    <button
      v-for="c in LABEL_COLORS"
      :key="c"
      type="button"
      role="radio"
      :aria-checked="model === c"
      :aria-label="c"
      :title="c"
      :data-color="c"
      :disabled="disabled"
      :class="
        cn(
          'size-6 rounded-full outline-none ring-offset-2 ring-offset-background focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60',
          labelSwatchClass(c),
          model === c && 'ring-2 ring-foreground',
        )
      "
      @click="model = c"
    />
  </div>
</template>
