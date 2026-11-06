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
}

export interface AppInfo {
  name: string
  version: string
  dataDir: string
}

export const ACTIVE_STATUSES: Status[] = ['INITIALIZING', 'DOWNLOADING', 'PAUSING', 'VERIFYING']
export const isActive = (s: Status) => ACTIVE_STATUSES.includes(s)
export const canResume = (s: Status) => s === 'PAUSED' || s === 'QUEUED' || s === 'FAILED'
export const canPause = (s: Status) => s === 'INITIALIZING' || s === 'DOWNLOADING'
export const canCancel = (s: Status) => s !== 'COMPLETED' && s !== 'CANCELLED' && s !== 'VERIFYING'
export const canDelete = (s: Status) => !isActive(s)
