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
  // Matches screenshot formatting (e.g. 745 KB, 3.00 MB, 12.00 MB, 2 MB, 2.5 GB)
  let decimals = 2
  if (v >= 100 || v % 1 === 0) {
    decimals = 0
  } else if (v >= 10) {
    decimals = v % 1 === 0 ? 0 : 2
  }
  return `${v.toFixed(decimals)} ${UNITS[i]}`
}

export function formatSpeed(bytesPerSecond: number): string {
  if (!bytesPerSecond || bytesPerSecond < 0) return '—'
  return `${formatBytes(Math.round(bytesPerSecond))}/s`
}

export function formatEta(seconds: number): string {
  if (seconds === undefined || seconds === null || seconds < 0 || !Number.isFinite(seconds)) return '0 Sec'
  if (seconds <= 0) return '0 Sec'
  const s = Math.round(seconds)
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = s % 60
  if (h > 0) return `${h} Hr ${m} Min`
  if (m > 0) return `${m} Min ${sec} Sec`
  return `${sec} Sec`
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
    return 'Today'
  }
  const year = d.getFullYear()
  const month = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${year}/${month}/${day}`
}

export function formatMbs(bytesPerSecond: number): string {
  if (!bytesPerSecond || bytesPerSecond <= 0) return '0.00 Mbs'
  const mbps = (bytesPerSecond * 8) / 1_000_000
  return `${mbps.toFixed(2)} Mbs`
}

export function percentOf(done: number, total: number): number {
  if (total <= 0) return total === 0 ? 100 : -1
  return Math.min(100, (done / total) * 100)
}
