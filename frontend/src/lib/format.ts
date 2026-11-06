const UNITS = ['B', 'KB', 'MB', 'GB', 'TB']

/** Human-readable size using binary multiples; unknown (<0) shows a dash. */
export function formatBytes(n: number): string {
  if (n < 0 || !Number.isFinite(n)) return '—'
  if (n < 1024) return `${n} B`
  let i = 0
  let v = n
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2)} ${UNITS[i]}`
}

export function formatSpeed(bytesPerSecond: number): string {
  if (!bytesPerSecond || bytesPerSecond < 0) return '—'
  return `${formatBytes(Math.round(bytesPerSecond))}/s`
}

export function formatEta(seconds: number): string {
  if (seconds < 0 || !Number.isFinite(seconds)) return '—'
  if (seconds < 1) return 'now'
  const s = Math.round(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `${h}h ${m}m`
  if (m > 0) return `${m}m ${sec}s`
  return `${sec}s`
}

/** Go encodes zero times as year 1; treat those as unset. */
export function parseTime(iso: string): Date | null {
  if (!iso || iso.startsWith('0001-')) return null
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? null : d
}

export function formatDate(iso: string): string {
  const d = parseTime(iso)
  if (!d) return '—'
  const today = new Date()
  if (d.toDateString() === today.toDateString()) {
    return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  }
  return d.toLocaleDateString(undefined, {
    day: 'numeric', month: 'short', year: d.getFullYear() === today.getFullYear() ? undefined : 'numeric',
  })
}

export function percentOf(done: number, total: number): number {
  if (total <= 0) return total === 0 ? 100 : -1
  return Math.min(100, (done / total) * 100)
}
