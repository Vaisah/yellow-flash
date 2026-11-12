import type { Download, MediaRow, Progress, Status } from '../types'

/**
 * Video and gallery downloads (yt-dlp, gallery-dl) run outside the engine and
 * last for the session. They are shown in the list as rows of their own,
 * with ids "media:<id>" so they never clash with the engine's.
 */
const PREFIX = 'media:'

export const isMedia = (d: Download) => d.id.startsWith(PREFIX)
export const mediaId = (d: Download) => d.id.slice(PREFIX.length)

const STATUS: Record<MediaRow['progress']['state'], Status> = {
  running: 'DOWNLOADING',
  done: 'COMPLETED',
  failed: 'FAILED',
  stopped: 'CANCELLED',
}

function baseName(path: string): string {
  return path.split(/[\\/]/).pop() ?? path
}

/** The row's name: the saved file once there is one, else the title. */
function nameOf(r: MediaRow): string {
  if (r.progress.file) return baseName(r.progress.file)
  return r.title || r.url
}

export function mediaDownload(r: MediaRow): Download {
  const p = r.progress
  const file = p.file ?? ''
  return {
    id: PREFIX + r.id,
    url: r.url,
    finalUrl: r.url,
    filename: nameOf(r),
    filenameExplicit: true,
    destination: file ? file.slice(0, file.length - baseName(file).length).replace(/[\\/]$/, '') : r.dir,
    tempPath: '',
    mimeType: '',
    etag: '',
    lastModified: '',
    error: p.error ?? '',
    size: p.total > 0 ? p.total : p.state === 'done' ? p.downloaded : -1,
    downloaded: p.downloaded,
    rangeSupported: false,
    connections: 1,
    retries: 0,
    status: STATUS[p.state] ?? 'DOWNLOADING',
    media: r.kind,
    createdAt: r.started,
    startedAt: r.started,
    completedAt: '',
    updatedAt: r.started,
  }
}

export function mediaProgress(r: MediaRow): Progress | null {
  const p = r.progress
  if (p.state !== 'running') return null
  const total = p.total > 0 ? p.total : -1
  const speed = p.speed > 0 ? p.speed : 0
  return {
    downloadId: PREFIX + r.id,
    downloaded: p.downloaded,
    total,
    percent: total > 0 ? (p.downloaded / total) * 100 : -1,
    speed,
    averageSpeed: speed,
    etaSeconds: total > 0 && speed > 0 ? Math.max(0, (total - p.downloaded) / speed) : -1,
    activeConnections: 1,
  }
}
