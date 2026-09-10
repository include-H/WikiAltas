const STORAGE_KEY = 'wikiatlas-theme'

export type ThemeMode = 'light' | 'dark'

export function getStoredTheme(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  if (saved === 'light' || saved === 'dark') return saved
  if (window.matchMedia?.('(prefers-color-scheme: dark)').matches) return 'dark'
  return 'light'
}

export function applyTheme(mode: ThemeMode): void {
  document.body.setAttribute('theme-mode', mode === 'dark' ? 'dark' : 'light')
  localStorage.setItem(STORAGE_KEY, mode)
}

export function toggleTheme(): ThemeMode {
  const next: ThemeMode = document.body.getAttribute('theme-mode') === 'dark' ? 'light' : 'dark'
  applyTheme(next)
  return next
}
