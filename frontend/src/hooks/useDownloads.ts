import { useCallback, useEffect, useState } from 'react'
import { backend, errorMessage, onDownloadUpdated, onProgress } from '../api'
import { isActive } from '../types'
import type { Download, Progress } from '../types'

/**
 * Keeps the download list in sync with the engine: one initial load, then
 * live updates from "downloads:updated" and throttled "downloads:progress"
 * events. The list is reloaded when the window regains focus as a safety net.
 */
export function useDownloads() {
  const [items, setItems] = useState<Download[]>([])
  const [progress, setProgress] = useState<Record<string, Progress>>({})
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState('')

  const refresh = useCallback(async () => {
    try {
      const list = await backend().ListDownloads()
      setItems(list ?? [])
      setLoadError('')
    } catch (e) {
      setLoadError(errorMessage(e))
    } finally {
      setLoaded(true)
    }
  }, [])

  useEffect(() => {
    void refresh()
    const offUpdated = onDownloadUpdated((d) => {
      setItems((prev) => {
        const i = prev.findIndex((x) => x.id === d.id)
        if (i < 0) return [d, ...prev]
        const next = prev.slice()
        next[i] = d
        return next
      })
      if (!isActive(d.status)) {
        setProgress((p) => {
          if (!(d.id in p)) return p
          const { [d.id]: _gone, ...rest } = p
          return rest
        })
      }
    })
    const offProgress = onProgress((p) => setProgress((prev) => ({ ...prev, [p.downloadId]: p })))
    const onFocus = () => void refresh()
    window.addEventListener('focus', onFocus)
    return () => {
      offUpdated()
      offProgress()
      window.removeEventListener('focus', onFocus)
    }
  }, [refresh])

  const removeLocal = useCallback((id: string) => setItems((prev) => prev.filter((d) => d.id !== id)), [])
  const upsertLocal = useCallback((d: Download) => {
    setItems((prev) => (prev.some((x) => x.id === d.id) ? prev.map((x) => (x.id === d.id ? d : x)) : [d, ...prev]))
  }, [])

  return { items, progress, loaded, loadError, refresh, removeLocal, upsertLocal }
}
