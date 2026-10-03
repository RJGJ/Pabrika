import { safeGet } from './storage'

export type Theme = 'light' | 'dark' | 'system'
export const THEME_KEY = 'pabrika.theme'

export function readStoredTheme(): Theme {
  const v = safeGet(THEME_KEY)
  return v === 'light' || v === 'dark' || v === 'system' ? v : 'system'
}

export function prefersDark(): boolean {
  try {
    return typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-color-scheme: dark)').matches
  } catch {
    return false
  }
}

export function isDark(theme: Theme, systemDark: boolean): boolean {
  return theme === 'dark' || (theme === 'system' && systemDark)
}

/** Toggle the `dark` class on <html>. Called from main.ts before mount (no inline script: CSP). */
export function applyTheme(theme: Theme): void {
  document.documentElement.classList.toggle('dark', isDark(theme, prefersDark()))
}

/** Keep "system" in sync with the OS setting; returns an unsubscribe function. */
export function watchSystemTheme(getTheme: () => Theme): () => void {
  try {
    const mq = window.matchMedia('(prefers-color-scheme: dark)')
    const fn = () => {
      if (getTheme() === 'system') applyTheme('system')
    }
    mq.addEventListener('change', fn)
    return () => mq.removeEventListener('change', fn)
  } catch {
    return () => {}
  }
}
