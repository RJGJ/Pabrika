<script setup lang="ts">
import { RouterLink } from 'vue-router'
import EmptyState from '@/components/common/EmptyState.vue'
import { Button } from '@/components/ui/button'
</script>

<template>
  <EmptyState title="Page not found" description="The page you are looking for does not exist.">
    <Button as-child><RouterLink to="/">Go home</RouterLink></Button>
  </EmptyState>
</template>
