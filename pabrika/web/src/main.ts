import { createApp } from 'vue'
import { createPinia } from 'pinia'
import './style.css'
import App from './App.vue'
import { configureClient } from '@/api/client'
import { notify } from '@/lib/toast'
import { applyTheme, watchSystemTheme } from '@/lib/theme'
import { createAppRouter } from '@/router'
import { setRouter } from '@/router/instance'
import { useAuthStore } from '@/stores/auth'
import { useBoardStore } from '@/stores/board'
import { useUiStore } from '@/stores/ui'

const app = createApp(App)
const pinia = createPinia()
app.use(pinia)

const router = createAppRouter()
setRouter(router)
app.use(router)

configureClient({
  onUnauthorized: () => useAuthStore().handleUnauthorized(),
  onToast: notify,
  onForbidden: () => void useBoardStore().refetchProject(),
})

// Apply the theme before mount (no inline script because of the CSP; a brief flash is accepted).
const ui = useUiStore()
applyTheme(ui.theme)
watchSystemTheme(() => ui.theme)

void router.isReady().then(() => app.mount('#app'))
