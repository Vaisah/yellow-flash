import { useEffect, useState } from 'react'
import { AlertTriangle, ChevronDown, Copy, ExternalLink, FolderOpen } from 'lucide-react'
import { backend } from '../api'
import { iconFor } from '../lib/categories'
import { explainError } from '../lib/errors'
import { formatBytes, formatDate, formatEta, formatSpeed, percentOf } from '../lib/format'
import { isActive } from '../types'
import type { Download, Progress, Segment, ServerLocation } from '../types'

/** The flags' picture, asked of the backend once. */
let flagsPicture: Promise<string> | null = null

function countryName(code: string): string {
  try {
    return new Intl.DisplayNames([navigator.language || 'en'], { type: 'region' }).of(code) ?? code
  } catch {
    return code
  }
}

/** Where the download's server is: its flag and country (DB-IP's table, looked up on this computer), how far. */
function ServerPlace({ d }: { d: Download }) {
  const [loc, setLoc] = useState<ServerLocation | null>(null)
  const [flags, setFlags] = useState('')
  useEffect(() => {
    let current = true
    backend().ServerLocation(d.id).then((l) => { if (current) setLoc(l) }).catch(() => undefined)
    return () => { current = false }
  }, [d.id, d.server?.ip, d.server?.rttMs])
  useEffect(() => {
    if (!loc?.flag) return
    flagsPicture ??= backend().FlagsPicture()
    flagsPicture.then(setFlags).catch(() => undefined)
  }, [loc?.flag])
  if (!loc) return <>—</>
  const place = loc.place === 'country' && loc.country ? countryName(loc.country)
    : loc.place === 'this-computer' ? 'This computer'
    : loc.place === 'local-network' ? 'The local network' : 'Unknown'
  const via = [loc.cdn && `via ${loc.cdn}`, loc.proxied && 'through a proxy'].filter(Boolean).join(', ')
  const tip = [loc.ip, via, loc.place === 'country' ? `${loc.attribution} (${loc.attributionUrl})` : ''].filter(Boolean).join(' · ')
  return (
    <span title={tip} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      {loc.flag && flags && (
        <span
          aria-hidden="true"
          style={{
            width: 20, height: 15, display: 'inline-block', borderRadius: 2,
            backgroundImage: `url(${flags})`,
            backgroundPosition: `-${loc.flag.x * 20 / loc.flag.w}px -${loc.flag.y * 15 / loc.flag.h}px`,
            backgroundSize: `${16 * 20}px auto`,
          }}
        />
      )}
      {place}
      {loc.rttMs ? ` · ${loc.reach} (${loc.rttMs} ms)` : ''}
      {via ? ` · ${via}` : ''}
    </span>
  )
}
import { statusLabel } from './DownloadTable'

interface Props {
  download: Download
  progress?: Progress
  onCollapse: () => void
  onOpenFile: (d: Download) => void
  onShowInFolder: (d: Download) => void
  onCopyLink: (d: Download) => void
}

/** Shows everything about the selected download, including a segment map. */
export function DetailsPanel({ download: d, progress: p, onCollapse, onOpenFile, onShowInFolder, onCopyLink }: Props) {
  const [segments, setSegments] = useState<Segment[]>([])
  const active = isActive(d.status)

  useEffect(() => {
    let cancelled = false
    const load = async () => {
      try {
        const segs = await backend().GetSegments(d.id)
        if (!cancelled) setSegments(segs ?? [])
      } catch {
        if (!cancelled) setSegments([])
      }
    }
    void load()
    if (!active) return () => { cancelled = true }
    const timer = window.setInterval(load, 1000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [d.id, active])

  const done = p?.downloaded ?? d.downloaded
  const pct = percentOf(done, d.size)
  const Icon = iconFor(d.filename)
  const failure = d.status === 'FAILED' && d.error ? explainError(d.error) : null

  return (
    <section className="details" aria-label="Download details">
      <header className="details-head">
        <span className={`file-badge big status-${d.status.toLowerCase()}`}><Icon size={20} /></span>
        <div className="details-title">
          <strong title={d.filename}>{d.filename}</strong>
          <span>{statusLabel(d, pct)}</span>
        </div>
        <div className="details-actions">
          <button className="btn btn-sm" disabled={d.status !== 'COMPLETED'} onClick={() => onOpenFile(d)}><ExternalLink size={14} /> Open</button>
          <button className="btn btn-sm" onClick={() => onShowInFolder(d)}><FolderOpen size={14} /> Show in folder</button>
          <button className="btn btn-sm" onClick={() => onCopyLink(d)}><Copy size={14} /> Copy link</button>
          <button className="icon-btn" onClick={onCollapse} aria-label="Hide details"><ChevronDown size={16} /></button>
        </div>
      </header>

      {failure && (
        <div className="failure" role="alert">
          <AlertTriangle size={16} />
          <div>
            <strong>{failure.title}</strong>
            <p>{failure.hint}</p>
            {failure.hint !== d.error && <code>{d.error}</code>}
          </div>
        </div>
      )}

      <SegmentMap download={d} segments={segments} done={done} />

      <dl className="facts">
        <div><dt>Downloaded</dt><dd>{formatBytes(done)} of {formatBytes(d.size)}{pct >= 0 ? ` (${pct.toFixed(1)}%)` : ''}</dd></div>
        <div><dt>Speed</dt><dd>{p ? `${formatSpeed(p.speed)} · avg ${formatSpeed(p.averageSpeed)}` : '—'}</dd></div>
        <div><dt>Time left</dt><dd>{p ? formatEta(p.etaSeconds) : '—'}</dd></div>
        <div><dt>Connections</dt><dd>{p ? `${p.activeConnections} active of ${d.connections}` : d.connections}</dd></div>
        <div><dt>Resume</dt><dd>{d.status === 'QUEUED' ? 'Not checked yet' : d.rangeSupported ? 'Supported' : 'Not supported'}</dd></div>
        <div><dt>Retries</dt><dd>{d.retries}</dd></div>
        <div><dt>Server</dt><dd>{d.server ? <ServerPlace d={d} /> : '—'}</dd></div>
        <div><dt>Added</dt><dd>{formatDate(d.createdAt)}</dd></div>
        <div><dt>Finished</dt><dd>{formatDate(d.completedAt)}</dd></div>
        <div className="wide"><dt>Saved to</dt><dd className="mono" title={d.destination}>{d.destination}</dd></div>
        <div className="wide"><dt>Address</dt><dd className="mono" title={d.url}>{d.url}</dd></div>
      </dl>
    </section>
  )
}

/** One lane per connection range, like a classic download manager's segment view. */
function SegmentMap({ download: d, segments, done }: { download: Download; segments: Segment[]; done: number }) {
  if (d.status === 'COMPLETED') {
    return <div className="segmap" aria-hidden="true"><div className="seg" style={{ left: 0, width: '100%' }}><div className="seg-fill" style={{ width: '100%' }} /></div></div>
  }
  if (d.size <= 0 || segments.length === 0) {
    const pct = percentOf(done, d.size)
    const running = isActive(d.status)
    // Unknown size: animate while running, otherwise show an empty bar.
    const width = pct >= 0 ? pct : running ? 100 : 0
    return (
      <div className="segmap" aria-hidden="true">
        <div className="seg" style={{ left: 0, width: '100%' }}>
          <div className={`seg-fill ${pct < 0 && running ? 'indeterminate' : ''}`} style={{ width: `${width}%` }} />
        </div>
      </div>
    )
  }
  return (
    <div className="segmap" aria-label={`${segments.length} segments`}>
      {segments.map((s) => {
        const len = s.end - s.start + 1
        return (
          <div key={s.id} className="seg" title={`Segment ${s.index + 1}: ${formatBytes(s.downloaded)} of ${formatBytes(len)}`}
            style={{ left: `${(s.start / d.size) * 100}%`, width: `${(len / d.size) * 100}%` }}>
            <div className="seg-fill" style={{ width: `${Math.min(100, (s.downloaded / len) * 100)}%` }} />
          </div>
        )
      })}
    </div>
  )
}
