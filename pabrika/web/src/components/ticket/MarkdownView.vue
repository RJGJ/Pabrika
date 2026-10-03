<script setup lang="ts">
import { computed } from 'vue'
import { renderMarkdown } from '@/lib/markdown'

const props = defineProps<{ source: string }>()
// renderMarkdown returns sanitized HTML (markdown-it with html disabled, then DOMPurify).
// This is the only place in the app that renders raw HTML.
const html = computed(() => renderMarkdown(props.source))
</script>

<template>
  <div class="md" v-html="html" />
</template>

<style scoped>
.md {
  font-size: 0.875rem;
  line-height: 1.5;
  overflow-wrap: anywhere;
}
.md :deep(> * + *) {
  margin-top: 0.5rem;
}
.md :deep(h1),
.md :deep(h2),
.md :deep(h3),
.md :deep(h4) {
  font-weight: 600;
  margin-top: 0.75rem;
}
.md :deep(h1) {
  font-size: 1.15rem;
}
.md :deep(h2) {
  font-size: 1.05rem;
}
.md :deep(ul) {
  list-style: disc;
  padding-left: 1.25rem;
}
.md :deep(ol) {
  list-style: decimal;
  padding-left: 1.25rem;
}
.md :deep(a) {
  text-decoration: underline;
  text-underline-offset: 2px;
}
.md :deep(code) {
  background: var(--muted);
  border-radius: 0.25rem;
  padding: 0.05rem 0.3rem;
  font-size: 0.8rem;
}
.md :deep(pre) {
  background: var(--muted);
  border-radius: 0.375rem;
  padding: 0.5rem 0.75rem;
  overflow-x: auto;
}
.md :deep(pre code) {
  padding: 0;
  background: transparent;
}
.md :deep(blockquote) {
  border-left: 3px solid var(--border);
  padding-left: 0.75rem;
  color: var(--muted-foreground);
}
.md :deep(table) {
  border-collapse: collapse;
}
.md :deep(th),
.md :deep(td) {
  border: 1px solid var(--border);
  padding: 0.2rem 0.5rem;
}
</style>
