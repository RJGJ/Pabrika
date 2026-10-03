import type { Router } from 'vue-router'

// Stores need the router for redirects but the router imports stores for guards. This registry
// breaks the import cycle: main.ts (or a test) registers the router once.
let current: Router | null = null

export function setRouter(r: Router): void {
  current = r
}

export function getRouter(): Router | null {
  return current
}
