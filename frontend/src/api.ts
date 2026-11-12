import type { AppBindings } from './wails'
import type { Download, Progress } from './types'

/** The Go backend. Throws a clear error if the Wails runtime is missing. */
export function backend(): AppBindings {
  const app = window.go?.main?.App
  if (!app) throw new Error('Yellow Flash backend is not available')
  return app
}

export function onDownloadUpdated(cb: (d: Download) => void): () => void {
  return window.runtime?.EventsOn('downloads:updated', cb) ?? (() => {})
}

export function onProgress(cb: (p: Progress) => void): () => void {
  return window.runtime?.EventsOn('downloads:progress', cb) ?? (() => {})
}

/** A link handed over by a second launch (`/d URL [/f name] [/p dir]`): open Add URL with it. */
export interface AddPrefill { url: string; filename: string; directory: string }

export function onAddRequest(cb: (p: AddPrefill) => void): () => void {
  return window.runtime?.EventsOn('app:add-url', cb) ?? (() => {})
}

/** The browser's video button: open the Download video window on this page. */
export interface VideoPrefill { url: string }

export function onVideoRequest(cb: (p: VideoPrefill) => void): () => void {
  return window.runtime?.EventsOn('app:video', cb) ?? (() => {})
}

/** A message from the backend to show as a toast (a silent add's result). */
export interface Notice { kind: 'ok' | 'error' | 'info'; text: string }

export function onNotice(cb: (n: Notice) => void): () => void {
  return window.runtime?.EventsOn('app:notice', cb) ?? (() => {})
}

/** Any backend event ("media:progress", "extract:done", "torrents:progress", …). */
export function onEvent<T = unknown>(name: string, cb: (data: T) => void): () => void {
  return window.runtime?.EventsOn(name, cb) ?? (() => {})
}

/** Opens an address in the system's browser. */
export function openInBrowser(url: string): void {
  if (window.runtime?.BrowserOpenURL) window.runtime.BrowserOpenURL(url)
  else window.open(url, '_blank', 'noopener')
}

/** Wails rejects with plain strings; normalise to a message. */
export function errorMessage(e: unknown): string {
  if (typeof e === 'string') return e
  if (e instanceof Error) return e.message
  return 'Something went wrong'
}

export async function copyText(text: string): Promise<void> {
  if (window.runtime?.ClipboardSetText) {
    await window.runtime.ClipboardSetText(text)
    return
  }
  await navigator.clipboard.writeText(text)
}
