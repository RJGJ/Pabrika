<script setup lang="ts">
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="flex flex-col items-center justify-center gap-3 p-10 text-center">
    <h2 class="text-lg font-semibold">{{ title }}</h2>
    <p v-if="description" class="max-w-md text-sm text-muted-foreground">{{ description }}</p>
    <slot />
  </div>
</template>
