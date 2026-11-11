/**
 * Development-only stand-in for the Go backend, so `npm run dev` works in a
 * normal browser. It simulates segmented progress and the same events the
 * real engine emits. Never bundled into production builds.
 */
import type { AppBindings } from '../wails'
import type { Download, LinkInfo, Progress, Segment, Settings, Status } from '../types'

type Listener = (...data: unknown[]) => void
const ZERO = '0001-01-01T00:00:00Z'
const KB = 1024
const MB = 1024 * 1024
const GB = 1024 * MB

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
  const date2023 = new Date(2023, 7, 9, 14, 30).toISOString()
  const today = new Date().toISOString()

  const make = (p: Partial<Download> & { filename: string; url: string; status: Status }): Download => ({
    id: `mock-${++seq}`,
    finalUrl: p.url,
    filenameExplicit: false,
    destination: 'C:\\Downloads',
    tempPath: `C:\\Downloads\\${p.filename}.part`,
    mimeType: '',
    etag: '"abc"',
    lastModified: '',
    error: '',
    size: -1,
    downloaded: 0,
    rangeSupported: true,
    connections: 8,
    retries: 0,
    createdAt: date2023,
    startedAt: ZERO,
    completedAt: ZERO,
    updatedAt: date2023,
    ...p,
  })

  // Items precisely mirroring the UI showcase in the user's reference design
  const items: Download[] = [
    make({
      filename: 'UIUXMonster.png',
      url: 'https://cdn.example.com/assets/UIUXMonster.png',
      status: 'COMPLETED',
      size: 745 * KB,
      downloaded: 745 * KB,
      createdAt: date2023,
      completedAt: date2023,
    }),
    make({
      filename: '2pacCover.mp3',
      url: 'https://music.example.com/tracks/2pacCover.mp3',
      status: 'DOWNLOADING',
      size: 3 * MB,
      downloaded: Math.round(3 * MB * 0.8),
      createdAt: date2023,
      startedAt: date2023,
    }),
    make({
      filename: 'UIUXMonster.Zip',
      url: 'https://downloads.example.com/zips/UIUXMonster.Zip',
      status: 'COMPLETED',
      size: 12 * MB,
      downloaded: 12 * MB,
      createdAt: date2023,
      completedAt: date2023,
    }),
    make({
      filename: 'Details.pdf',
      url: 'https://docs.example.com/files/Details.pdf',
      status: 'DOWNLOADING',
      size: 2 * MB,
      downloaded: 1 * MB,
      createdAt: date2023,
      startedAt: date2023,
    }),
    make({
      filename: 'Better.Call Saul.so2E10.720p.mp3',
      url: 'https://media.example.com/series/Better.Call.Saul.so2E10.720p.mp3',
      status: 'QUEUED',
      size: 2.5 * GB,
      downloaded: 0,
      createdAt: date2023,
    }),
    make({
      filename: 'Call Of Duty.apk',
      url: 'https://apks.example.com/games/Call_Of_Duty.apk',
      status: 'PAUSED',
      size: 12 * MB,
      downloaded: Math.round(12 * MB * 0.45),
      createdAt: date2023,
    }),
    make({
      filename: '2pacCover.mp3',
      url: 'https://archive.example.com/audio/2pacCover.mp3',
      status: 'DOWNLOADING',
      size: 12 * MB,
      downloaded: Math.round(12 * MB * 0.1),
      createdAt: today,
      startedAt: today,
    }),
    make({
      filename: 'Mima.exe',
      url: 'https://software.example.com/installers/Mima.exe',
      status: 'COMPLETED',
      size: 32 * MB,
      downloaded: 32 * MB,
      createdAt: date2023,
      completedAt: date2023,
    }),
  ]

  const speeds = new Map<string, number>([
    [items[1].id, 1.4 * MB],
    [items[3].id, 0.4 * MB],
    [items[6].id, 1.1 * MB],
  ])
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
      const base = speeds.get(d.id) ?? 1.2 * MB
      const speed = base * (0.9 + Math.random() * 0.2)
      d.downloaded = Math.min(d.size, d.downloaded + Math.round(speed / 4))
      const eta = d.id === items[3].id ? 312 : Math.max(0, (d.size - d.downloaded) / speed)
      const p: Progress = {
        downloadId: d.id,
        downloaded: d.downloaded,
        total: d.size,
        percent: (d.downloaded / d.size) * 100,
        speed,
        averageSpeed: base,
        etaSeconds: eta,
        activeConnections: d.connections,
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
  }, 350)

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
  let settings: Settings = { downloadDirectory: 'C:\\Downloads', connections: 8, startImmediately: true, useClipboard: true }

  const unwrap = (raw: string) => {
    try {
      const u = new URL(raw)
      return (u.hostname.includes('google.') && u.pathname === '/imgres' && u.searchParams.get('imgurl')) || raw
    } catch {
      return raw
    }
  }

  // The rest of the backend, simulated plainly for browser previews.
  let speed = { enabled: false, kbps: 2048 }
  let logins: { site: string; user: string }[] = []
  let options = { launchOnStartup: false, showComplete: true, browserCapture: true, fileTypes: 'ZIP RAR 7Z ISO EXE MSI MP4 MKV MP3 PDF' }
  let network = { speedLimitKBps: 0, dataCapEnabled: false, dataCapMB: 10000, dataCapHours: 24 }
  const queues = [
    { id: 'main', name: 'Main Download Queue', filesAtOnce: 4, items: [] as string[], schedule: { days: [true, true, true, true, true, true, true] }, createdAt: '', running: false, builtin: true },
    { id: 'sync', name: 'Synchronization Queue', filesAtOnce: 1, items: [] as string[], sync: true, schedule: { days: [true, true, true, true, true, true, true] }, createdAt: '', running: false, builtin: true },
  ]
  const queueOf = (id: string) => queues.find((q) => q.id === id) ?? Promise.reject('no such queue')
  const extras: Omit<AppBindings, 'Info' | 'ListDownloads' | 'GetDownload' | 'InspectLink' | 'AddDownload' | 'ResumeDownload' |
    'PauseDownload' | 'CancelDownload' | 'RetryDownload' | 'DeleteDownload' | 'PauseAll' | 'ResumeAll' | 'GetSegments' | 'OpenFile' |
    'ShowInFolder' | 'OpenFolder' | 'SelectDirectory' | 'ClipboardURL' | 'GetSettings' | 'SaveSettings'> = {
    ListQueues: () => later(queues.map((q) => ({ ...q, items: [...q.items] }))),
    CreateQueue: (name) => { const q = { ...queues[0], id: `q${Date.now()}`, name, items: [], builtin: false, running: false }; queues.push(q); return later(q) },
    DeleteQueue: async (id) => { const i = queues.findIndex((q) => q.id === id); if (i > 1) queues.splice(i, 1) },
    AddToQueue: async (qid, ids) => { const q = await queueOf(qid); q.items.push(...ids) },
    RemoveFromQueue: async (id) => { for (const q of queues) q.items = q.items.filter((x) => x !== id) },
    MoveInQueue: async (id, delta) => {
      for (const q of queues) {
        const i = q.items.indexOf(id)
        const j = i + delta
        if (i >= 0 && j >= 0 && j < q.items.length) [q.items[i], q.items[j]] = [q.items[j], q.items[i]]
      }
    },
    StartQueue: async (id) => { (await queueOf(id)).running = true },
    StopQueue: async (id) => { (await queueOf(id)).running = false },
    SetQueueFilesAtOnce: async (id, n) => { (await queueOf(id)).filesAtOnce = n },
    SetQueueSchedule: async (id, sch) => { (await queueOf(id)).schedule = sch as typeof queues[0]['schedule'] },
    GetNetworkSettings: () => later({ ...network }),
    SaveNetworkSettings: (n) => { network = { ...network, ...n }; return later({ ...network }) },
    FileChecksum: () => later('e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855', 400),
    DiskSpace: (dir) => later({ dir, free: 120 * 1024 * MB, total: 512 * 1024 * MB, usedPercent: 77 }),
    CleanDisk: () => later({ files: 0, bytes: 0 }),
    GetSpeedLimit: () => later({ ...speed }),
    SetSpeedLimit: (l) => { speed = { ...l }; return later({ ...speed }) },
    ListSiteLogins: () => later([...logins]),
    SaveSiteLogin: (site, user) => { logins = [...logins.filter((l) => l.site !== site), { site, user }]; return later([...logins]) },
    RemoveSiteLogin: (site) => { logins = logins.filter((l) => l.site !== site); return later([...logins]) },
    GetOptions: () => later({ ...options }),
    SaveOptions: (o) => { options = { ...o }; return later({ ...options }) },
    GetBrowserSetup: () => later({
      browsers: [
        { name: 'Google Chrome', family: 'chromium' as const, connected: false, version: '', store: '', unpacked: true },
        { name: 'Microsoft Edge', family: 'chromium' as const, connected: true, version: '0.2.0', store: '', unpacked: true },
        { name: 'Mozilla Firefox', family: 'firefox' as const, connected: false, version: '', store: '', unpacked: false },
      ],
      extensionDir: 'C:\\Program Files\\Yellow Flash\\browser-extension', hostReady: true, portable: false, offer: false,
    }),
    AddExtensionTo: () => later('unpacked' as const),
    FinishBrowserSetup: async () => undefined,
    ServerLocation: () => later({ place: 'unknown' as const, attribution: 'IP Geolocation by DB-IP', attributionUrl: 'https://db-ip.com' }),
    FlagsPicture: () => later(''),
    ExtractDownload: async () => undefined,
    GetHelpers: () => later([{ name: 'yt-dlp', version: '', exe: '', chosen: false }]),
    InstallHelper: () => later({ name: 'yt-dlp', version: 'preview', exe: 'yt-dlp', chosen: false }, 800),
    LookUpVideo: () => later({ title: 'A video (browser preview)', duration: 213, needsFfmpeg: false, subs: [],
      choices: [{ label: 'Best (1080p)', selector: 'b', ext: 'mp4', size: 85 * MB }, { label: 'Audio only (MP3)', selector: 'mp3', ext: 'mp3', size: 8 * MB }] }, 600),
    StartVideoDownload: (req) => later({ id: `m${Date.now()}`, kind: 'video' as const, title: req.title, url: req.url,
      progress: { state: 'running' as const, downloaded: 0, total: 0, speed: 0 } }),
    ListMediaDownloads: () => later([]),
    StopMediaDownload: async () => undefined,
  }

  const app: AppBindings = {
    ...extras,
    Info: () => later({ name: 'Yellow Flash', version: '10.36', dataDir: 'C:\\Downloads' }),
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
    SelectDirectory: () => later('C:\\Downloads'),
    ClipboardURL: () => later(''),
    GetSettings: () => later({ ...settings }),
    async SaveSettings(s) {
      settings = { ...s }
      return { ...settings }
    },
  }
  window.go = { main: { App: app } }
}
