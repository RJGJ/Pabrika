import { defineStore } from 'pinia'
import { ref } from 'vue'
import { applyTheme, readStoredTheme, THEME_KEY, type Theme } from '@/lib/theme'
import { safeSet } from '@/lib/storage'

export const useUiStore = defineStore('ui', () => {
  const theme = ref<Theme>(readStoredTheme())
  /** The shadcn Sidebar keeps its own cookie state; this mirrors it for other components. */
  const sidebarOpen = ref(true)

  function setTheme(next: Theme) {
    theme.value = next
    safeSet(THEME_KEY, next)
    applyTheme(next)
  }

  return { theme, sidebarOpen, setTheme }
})
