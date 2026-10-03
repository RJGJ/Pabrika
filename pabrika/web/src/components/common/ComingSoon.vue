<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<template>
  <div class="mx-auto max-w-xl p-8">
    <h1 class="text-xl font-semibold">{{ title }}</h1>
    <p class="mt-2 text-sm text-muted-foreground">This screen is coming soon.</p>
  </div>
</template>
