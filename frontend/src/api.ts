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
