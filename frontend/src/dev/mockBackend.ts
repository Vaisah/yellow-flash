/**
 * Development-only stand-in for the Go backend, so `npm run dev` works in a
 * normal browser. It simulates segmented progress and the same events the
 * real engine emits. Never bundled into production builds.
 */
import type { AppBindings } from '../wails'
import type { Download, LinkInfo, Progress, Segment, Settings, Status } from '../types'

type Listener = (...data: unknown[]) => void
const ZERO = '0001-01-01T00:00:00Z'
const MB = 1024 * 1024

export function installMockBackend() {
  const listeners = new Map<string, Set<Listener>>()
  const emit = (event: string, data: unknown) => listeners.get(event)?.forEach((cb) => cb(data))
  window.runtime = {
    EventsOn(event, cb) {
      if (!listeners.has(event)) listeners.set(event, new Set())
      listeners.get(event)!.add(cb)
      return () => listeners.get(event)?.delete(cb)
    },
    async ClipboardSetText(text) {
      await navigator.clipboard?.writeText(text).catch(() => undefined)
      return true
    },
  }

  let seq = 0
  const now = Date.now()
  const iso = (offsetMin: number) => new Date(now - offsetMin * 60_000).toISOString()
  const make = (p: Partial<Download> & { filename: string; url: string; status: Status }): Download => ({
    id: `mock-${++seq}`, finalUrl: p.url, filenameExplicit: false, destination: 'C:\\Users\\you\\Downloads',
    tempPath: `C:\\Users\\you\\Downloads\\${p.filename}.part`, mimeType: '', etag: '"abc"', lastModified: '', error: '',
    size: -1, downloaded: 0, rangeSupported: true, connections: 8, retries: 0, createdAt: iso(5), startedAt: ZERO,
    completedAt: ZERO, updatedAt: iso(0), ...p,
  })

  const items: Download[] = [
    make({ filename: 'ubuntu-24.04.1-desktop-amd64.iso', url: 'https://releases.ubuntu.com/24.04/ubuntu-24.04.1-desktop-amd64.iso', status: 'DOWNLOADING', size: 5900 * MB, downloaded: 2200 * MB, createdAt: iso(12), startedAt: iso(12) }),
    make({ filename: 'Big Buck Bunny 1080p.mp4', url: 'https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_1080p_h264.mov', status: 'DOWNLOADING', size: 263 * MB, downloaded: 190 * MB, connections: 4, createdAt: iso(3), startedAt: iso(3) }),
    make({ filename: 'project-archive.zip', url: 'https://files.example.com/backups/project-archive.zip', status: 'PAUSED', size: 1229 * MB, downloaded: 664 * MB, createdAt: iso(90) }),
    make({ filename: 'report-final.pdf', url: 'https://docs.example.com/report-final.pdf', status: 'QUEUED', createdAt: iso(1) }),
    make({ filename: 'node-v22.11.0-x64.msi', url: 'https://nodejs.org/dist/v22.11.0/node-v22.11.0-x64.msi', status: 'COMPLETED', size: 28 * MB, downloaded: 28 * MB, createdAt: iso(300), completedAt: iso(298) }),
    make({ filename: 'Valve-SteamDeck.jpg', url: 'https://static0.xdaimages.com/wordpress/wp-content/uploads/2021/07/Valve-SteamDeck.jpg', status: 'COMPLETED', size: 62811, downloaded: 62811, createdAt: iso(1500), completedAt: iso(1500) }),
    make({ filename: 'A Monster\'s Expedition [1.03].pkg', url: 'https://archive.org/download/collection/A%20Monster%27s%20Expedition.pkg', status: 'FAILED', error: 'metadata: HTTP 403 Forbidden', createdAt: iso(2000) }),
    make({ filename: 'watch', url: 'https://www.youtube.com/watch?v=pdWh982UxQg', status: 'FAILED', error: 'the link points to a web page, not a downloadable file (server sent text/html; charset=utf-8)', createdAt: iso(2100) }),
  ]
  const speeds = new Map<string, number>([[items[0].id, 9.4 * MB], [items[1].id, 3.1 * MB]])
  const started = new Map<string, number>()

  const segmentsFor = (d: Download): Segment[] => {
    if (d.size <= 0 || !d.rangeSupported) return []
    const n = d.connections
    const len = Math.floor(d.size / n)
    const per = d.downloaded / n
    return Array.from({ length: n }, (_, i) => {
      const start = i * len
      const end = i === n - 1 ? d.size - 1 : start + len - 1
      const size = end - start + 1
      const jitter = 0.75 + ((i * 37) % 50) / 100
      const downloaded = Math.min(size, Math.round(per * jitter))
      return { id: `${d.id}-${i}`, downloadId: d.id, index: i, start, end, downloaded, status: downloaded >= size ? 'COMPLETED' : 'PENDING', retries: 0 }
    })
  }

  const publish = (d: Download) => emit('downloads:updated', { ...d })
  const setStatus = (d: Download, status: Status) => {
    d.status = status
    d.updatedAt = new Date().toISOString()
    publish(d)
  }

  window.setInterval(() => {
    for (const d of items) {
      if (d.status === 'INITIALIZING' && Date.now() - (started.get(d.id) ?? 0) > 700) {
        if (d.size < 0) d.size = Math.round((40 + Math.random() * 400) * MB)
        d.startedAt = new Date().toISOString()
        speeds.set(d.id, (2 + Math.random() * 8) * MB)
        setStatus(d, 'DOWNLOADING')
      }
      if (d.status !== 'DOWNLOADING') continue
      const base = speeds.get(d.id) ?? 4 * MB
      const speed = base * (0.8 + Math.random() * 0.4)
      d.downloaded = Math.min(d.size, d.downloaded + Math.round(speed / 4))
      const p: Progress = {
        downloadId: d.id, downloaded: d.downloaded, total: d.size, percent: (d.downloaded / d.size) * 100,
        speed, averageSpeed: base, etaSeconds: (d.size - d.downloaded) / speed, activeConnections: d.connections,
      }
      publish(d)
      emit('downloads:progress', p)
      if (d.downloaded >= d.size) {
        setStatus(d, 'VERIFYING')
        window.setTimeout(() => {
          d.completedAt = new Date().toISOString()
          setStatus(d, 'COMPLETED')
        }, 600)
      }
    }
  }, 250)

  const find = (id: string) => {
    const d = items.find((x) => x.id === id)
    if (!d) throw new Error('download not found')
    return d
  }
  const later = <T,>(value: T, ms = 120) => new Promise<T>((resolve) => window.setTimeout(() => resolve(value), ms))
  const transitionError = (from: Status, to: string) => Promise.reject(`invalid download state transition: ${from} -> ${to}`)
  const start = (d: Download) => {
    d.error = ''
    started.set(d.id, Date.now())
    setStatus(d, 'INITIALIZING')
  }
  let settings: Settings = { downloadDirectory: 'C:\\Users\\you\\Downloads', connections: 8, startImmediately: true, useClipboard: true }

  const unwrap = (raw: string) => {
    try {
      const u = new URL(raw)
      return (u.hostname.includes('google.') && u.pathname === '/imgres' && u.searchParams.get('imgurl')) || raw
    } catch {
      return raw
    }
  }

  const app: AppBindings = {
    Info: () => later({ name: 'Yellow Flash', version: '0.2.0-dev', dataDir: 'C:\\Users\\you\\AppData\\Roaming\\Yellow Flash' }),
    ListDownloads: () => later(items.map((d) => ({ ...d }))),
    GetDownload: async (id) => ({ ...find(id) }),
    async InspectLink(raw) {
      await later(null, 500)
      const url = unwrap(raw)
      const u = new URL(url)
      if (/youtube\.com$/.test(u.hostname.replace(/^www\./, '')) && u.pathname === '/watch') {
        return { url, finalUrl: url, filename: 'watch', size: -1, mimeType: 'text/html; charset=utf-8', rangeSupported: false, webPage: true }
      }
      if (u.hostname.includes('forbidden')) throw 'metadata: HTTP 403 Forbidden'
      const name = decodeURIComponent(u.pathname.split('/').pop() || 'download') || 'download'
      const info: LinkInfo = { url, finalUrl: url, filename: name, size: Math.round((5 + Math.random() * 900) * MB), mimeType: 'application/octet-stream', rangeSupported: true, webPage: false }
      return info
    },
    async AddDownload(req) {
      const url = unwrap(req.url)
      const name = req.filename || decodeURIComponent(new URL(url).pathname.split('/').pop() || 'download')
      const d = make({ filename: name, url, status: 'QUEUED', connections: req.connections, destination: req.directory, createdAt: new Date().toISOString() })
      items.unshift(d)
      publish(d)
      if (req.start) start(d)
      return later({ ...d })
    },
    async ResumeDownload(id) {
      const d = find(id)
      if (!['QUEUED', 'PAUSED', 'FAILED'].includes(d.status)) return transitionError(d.status, 'INITIALIZING')
      start(d)
    },
    async PauseDownload(id) {
      const d = find(id)
      if (!['DOWNLOADING', 'INITIALIZING'].includes(d.status)) return transitionError(d.status, 'PAUSING')
      setStatus(d, 'PAUSED')
    },
    async CancelDownload(id) {
      const d = find(id)
      d.downloaded = 0
      setStatus(d, 'CANCELLED')
    },
    async RetryDownload(id) {
      const d = find(id)
      if (d.status !== 'FAILED') return transitionError(d.status, 'INITIALIZING')
      start(d)
    },
    async DeleteDownload(id) {
      const i = items.findIndex((d) => d.id === id)
      if (i >= 0) items.splice(i, 1)
    },
    async PauseAll() { items.filter((d) => d.status === 'DOWNLOADING' || d.status === 'INITIALIZING').forEach((d) => setStatus(d, 'PAUSED')) },
    async ResumeAll() { items.filter((d) => d.status === 'PAUSED' || d.status === 'QUEUED').forEach(start) },
    GetSegments: async (id) => segmentsFor(find(id)),
    OpenFile: async () => undefined,
    ShowInFolder: async () => undefined,
    OpenFolder: async () => undefined,
    SelectDirectory: () => later('D:\\Downloads'),
    ClipboardURL: () => later(''),
    GetSettings: () => later({ ...settings }),
    async SaveSettings(s) {
      settings = { ...s }
      return { ...settings }
    },
  }
  window.go = { main: { App: app } }
}
