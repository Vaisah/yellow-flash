export type Status =
  | 'QUEUED' | 'INITIALIZING' | 'DOWNLOADING' | 'PAUSING' | 'PAUSED'
  | 'VERIFYING' | 'COMPLETED' | 'FAILED' | 'CANCELLED'

/** Mirrors domain.Download (Go JSON tags). Unknown sizes are -1. */
export interface Download {
  id: string
  url: string
  finalUrl: string
  filename: string
  filenameExplicit: boolean
  destination: string
  tempPath: string
  mimeType: string
  etag: string
  lastModified: string
  error: string
  size: number
  downloaded: number
  rangeSupported: boolean
  connections: number
  retries: number
  status: Status
  queueId?: string
  /** Where its answers came from (its last run's). */
  server?: ServerInfo
  /** A video or gallery download (yt-dlp, gallery-dl): its row only (lib/media.ts). */
  media?: 'video' | 'gallery'
  createdAt: string
  startedAt: string
  completedAt: string
  updatedAt: string
}

/** Mirrors domain.Progress. Unknown values are -1. */
export interface Progress {
  downloadId: string
  downloaded: number
  total: number
  percent: number
  speed: number
  averageSpeed: number
  etaSeconds: number
  activeConnections: number
}

export interface Segment {
  id: string
  downloadId: string
  index: number
  start: number
  end: number
  downloaded: number
  status: 'PENDING' | 'COMPLETED' | 'FAILED'
  retries: number
}

export interface LinkInfo {
  url: string
  finalUrl: string
  filename: string
  size: number
  mimeType: string
  rangeSupported: boolean
  webPage: boolean
}

export interface Settings {
  downloadDirectory: string
  connections: number
  startImmediately: boolean
  useClipboard: boolean
}

export interface AddRequest {
  url: string
  directory: string
  filename: string
  connections: number
  start: boolean
  /** The server's login (kept in memory only). */
  username?: string
  password?: string
}

export interface ServerInfo {
  ip?: string
  rttMs?: number
  proxied?: boolean
  cdn?: string
}

/** Mirrors main.ServerLocation. */
export interface ServerLocation {
  place: 'country' | 'this-computer' | 'local-network' | 'unknown'
  ip?: string
  country?: string
  flag?: { x: number; y: number; w: number; h: number }
  rttMs?: number
  reach?: 'near' | 'medium' | 'far' | ''
  proxied?: boolean
  cdn?: string
  attribution: string
  attributionUrl: string
}

/* ---------- queues (mirrors queue.Queue / main.QueueView) ---------- */

export interface TimeOfDay { hour: number; minute: number; second: number }
export interface QueueDate { year: number; month: number; day: number }

export interface Schedule {
  startOnStartup?: boolean
  startAt?: TimeOfDay | null
  days: boolean[]
  stopAt?: TimeOfDay | null
  once?: QueueDate | null
  retries?: number | null
  openWhenDone?: string
  exitWhenDone?: boolean
  turnOff?: '' | 'shutdown' | 'sleep' | 'hibernate'
  forceClose?: boolean
  wake?: boolean
}

export interface Queue {
  id: string
  name: string
  filesAtOnce: number
  items: string[]
  sync?: boolean
  schedule: Schedule
  createdAt: string
  running: boolean
  builtin: boolean
}

/* ---------- the other dialogs ---------- */

export interface DiskInfo { dir: string; free: number; total: number; usedPercent: number }
export interface CleanResult { files: number; bytes: number }
export interface SpeedLimit { enabled: boolean; kbps: number }
export interface SiteLogin { site: string; user: string }

/** Options → General and File Types (main.Options). */
export interface Options {
  launchOnStartup: boolean
  showComplete: boolean
  browserCapture: boolean
  fileTypes: string
}

/** Mirrors main.NetworkSettings (only what the dialogs read). */
export interface NetworkSettings {
  speedLimitKBps: number
  dataCapEnabled: boolean
  dataCapMB: number
  dataCapHours: number
  [key: string]: unknown
}

/** A way to download a video (tools.Choice). */
export interface VideoChoice { label: string; selector: string; ext: string; size: number }

export interface VideoLookUp {
  title: string
  duration: number
  choices: VideoChoice[] | null
  subs: unknown[] | null
  needsFfmpeg: boolean
  list?: { title: string; total: number } | null
}

export interface HelperInfo { name: string; version: string; exe: string; chosen: boolean }

export interface MediaProgress {
  state: 'running' | 'done' | 'failed' | 'stopped'
  file?: string
  error?: string
  downloaded: number
  total: number
  speed: number
}

export interface MediaRow {
  id: string; kind: 'video' | 'gallery'; title: string; url: string; progress: MediaProgress
  /** Its folder, and when it started. */
  dir: string; started: string
}

export interface ExtractResult {
  id: string
  archive: string
  ok: boolean
  report?: { folder: string; files: number; bytes: number; renamed: number; skipped?: { name: string; why: string }[] }
  error?: string
  reason?: string
  trashed?: boolean
}

export interface AppInfo {
  name: string
  version: string
  dataDir: string
}

/** One installed browser in the browser setup window. */
export interface BrowserTarget {
  name: string
  family: 'chromium' | 'firefox'
  /** Its extension has reached Yellow Flash. */
  connected: boolean
  version: string
  /** The store page that adds the extension, once it is listed; "" before. */
  store: string
  /** It can load the extension from extensionDir (Developer mode → Load unpacked). */
  unpacked: boolean
}

export interface BrowserSetup {
  browsers: BrowserTarget[]
  extensionDir: string
  hostReady: boolean
  portable: boolean
  /** Open the window at start: nothing connected yet and not dismissed. */
  offer: boolean
}

export const ACTIVE_STATUSES: Status[] = ['INITIALIZING', 'DOWNLOADING', 'PAUSING', 'VERIFYING']
export const isActive = (s: Status) => ACTIVE_STATUSES.includes(s)
export const canResume = (s: Status) => s === 'PAUSED' || s === 'QUEUED' || s === 'FAILED'
export const canPause = (s: Status) => s === 'INITIALIZING' || s === 'DOWNLOADING'
export const canCancel = (s: Status) => s !== 'COMPLETED' && s !== 'CANCELLED' && s !== 'VERIFYING'
export const canDelete = (s: Status) => !isActive(s)
