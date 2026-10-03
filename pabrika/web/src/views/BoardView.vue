<script setup lang="ts">
// EXTENSION POINT (WP4/WP5/WP6): the board, ticket panel (route param `number`) and live updates
// mount here. Route: /p/:key/t/:number? (one record, so the panel never remounts the board).
import ComingSoon from '@/components/common/ComingSoon.vue'
</script>

<template>
  <ComingSoon title="Board" />
</template>
