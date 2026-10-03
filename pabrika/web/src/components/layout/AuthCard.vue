<script setup lang="ts">
import ThemeToggle from '@/components/layout/ThemeToggle.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'

defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="relative flex min-h-screen items-center justify-center p-4">
    <div class="absolute right-4 top-4"><ThemeToggle /></div>
    <Card class="w-full max-w-sm">
      <CardHeader>
        <p class="text-sm font-semibold tracking-tight text-muted-foreground">Pabrika</p>
        <CardTitle class="text-xl">{{ title }}</CardTitle>
        <CardDescription v-if="description">{{ description }}</CardDescription>
      </CardHeader>
      <CardContent><slot /></CardContent>
    </Card>
  </div>
</template>
