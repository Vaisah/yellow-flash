import { useEffect, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import { ArrowDown, ArrowUp, Copy, FolderOpen, Pause, Play, RotateCcw, Square, Trash2, ExternalLink } from 'lucide-react'
import { iconFor } from '../lib/categories'
import { explainError } from '../lib/errors'
import { formatBytes, formatDate, formatEta, formatSpeed, percentOf, parseTime } from '../lib/format'
import { canCancel, canDelete, canPause, canResume } from '../types'
import type { Download, Progress } from '../types'

export type SortKey = 'name' | 'size' | 'progress' | 'speed' | 'eta' | 'added'
export interface Sort { key: SortKey; dir: 'asc' | 'desc' }

export interface RowActions {
  resume(d: Download): void
  pause(d: Download): void
  cancel(d: Download): void
  retry(d: Download): void
  remove(d: Download): void
  openFile(d: Download): void
  showInFolder(d: Download): void
  copyLink(d: Download): void
}

interface Props {
  rows: Download[]
  progress: Record<string, Progress>
  selectedId: string | null
  sort: Sort
  onSort: (key: SortKey) => void
  onSelect: (id: string) => void
  onActivate: (d: Download) => void
  actions: RowActions
}

const COLUMNS: { key: SortKey; label: string; className: string }[] = [
  { key: 'name', label: 'File name', className: 'col-name' },
  { key: 'size', label: 'Size', className: 'col-size' },
  { key: 'progress', label: 'Status', className: 'col-status' },
  { key: 'speed', label: 'Speed', className: 'col-speed' },
  { key: 'eta', label: 'Time left', className: 'col-eta' },
  { key: 'added', label: 'Added', className: 'col-added' },
]

export function statusLabel(d: Download, pct: number): string {
  switch (d.status) {
    case 'DOWNLOADING': return pct >= 0 ? `${pct.toFixed(1)}%` : 'Downloading'
    case 'INITIALIZING': return 'Connecting…'
    case 'PAUSING': return 'Pausing…'
    case 'PAUSED': return pct > 0 ? `Paused · ${pct.toFixed(0)}%` : 'Paused'
    case 'QUEUED': return 'Queued'
    case 'VERIFYING': return 'Verifying…'
    case 'COMPLETED': return 'Complete'
    case 'FAILED': return explainError(d.error).title
    case 'CANCELLED': return 'Cancelled'
  }
}

function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

export function sortRows(rows: Download[], progress: Record<string, Progress>, sort: Sort): Download[] {
  const value = (d: Download): number | string => {
    const p = progress[d.id]
    switch (sort.key) {
      case 'name': return d.filename.toLowerCase()
      case 'size': return d.size
      case 'progress': return percentOf(p?.downloaded ?? d.downloaded, d.size)
      case 'speed': return p?.speed ?? 0
      case 'eta': return p && p.etaSeconds >= 0 ? p.etaSeconds : Number.MAX_SAFE_INTEGER
      case 'added': return parseTime(d.createdAt)?.getTime() ?? 0
    }
  }
  const factor = sort.dir === 'asc' ? 1 : -1
  return rows.slice().sort((a, b) => {
    const va = value(a)
    const vb = value(b)
    if (va < vb) return -1 * factor
    if (va > vb) return 1 * factor
    return a.id < b.id ? -1 : 1
  })
}

interface MenuState { x: number; y: number; d: Download }

export function DownloadTable({ rows, progress, selectedId, sort, onSort, onSelect, onActivate, actions }: Props) {
  const [menu, setMenu] = useState<MenuState | null>(null)

  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(null)
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && close()
    window.addEventListener('click', close)
    window.addEventListener('blur', close)
    window.addEventListener('keydown', onKey)
    return () => {
      window.removeEventListener('click', close)
      window.removeEventListener('blur', close)
      window.removeEventListener('keydown', onKey)
    }
  }, [menu])

  const openMenu = (e: ReactMouseEvent, d: Download) => {
    e.preventDefault()
    onSelect(d.id)
    setMenu({ x: Math.min(e.clientX, window.innerWidth - 220), y: Math.min(e.clientY, window.innerHeight - 300), d })
  }

  const run = (fn: (d: Download) => void) => () => {
    if (menu) fn(menu.d)
    setMenu(null)
  }

  return (
    <div className="table-wrap">
      <table className="downloads" aria-label="Downloads">
        <thead>
          <tr>
            {COLUMNS.map((c) => (
              <th key={c.key} className={c.className} aria-sort={sort.key === c.key ? (sort.dir === 'asc' ? 'ascending' : 'descending') : 'none'}>
                <button onClick={() => onSort(c.key)}>
                  {c.label}
                  {sort.key === c.key && (sort.dir === 'asc' ? <ArrowUp size={12} /> : <ArrowDown size={12} />)}
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((d) => {
            const p = progress[d.id]
            const done = p?.downloaded ?? d.downloaded
            const pct = percentOf(done, d.size)
            const Icon = iconFor(d.filename)
            const status = d.status.toLowerCase()
            return (
              <tr
                key={d.id}
                className={`row status-${status} ${selectedId === d.id ? 'selected' : ''}`}
                aria-selected={selectedId === d.id}
                tabIndex={selectedId === d.id ? 0 : -1}
                onClick={() => onSelect(d.id)}
                onDoubleClick={() => onActivate(d)}
                onContextMenu={(e) => openMenu(e, d)}
              >
                <td className="col-name">
                  <div className="name-cell">
                    <span className={`file-badge status-${status}`}><Icon size={16} /></span>
                    <div className="name-text">
                      <strong title={d.filename}>{d.filename}</strong>
                      <span title={d.url}>{hostOf(d.url)}</span>
                    </div>
                  </div>
                </td>
                <td className="col-size num">{formatBytes(d.size)}</td>
                <td className="col-status">
                  <div className="status-cell">
                    <div className="bar" role="progressbar" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct >= 0 ? Math.round(pct) : undefined}>
                      <div className={`bar-fill ${pct < 0 && d.status === 'DOWNLOADING' ? 'indeterminate' : ''}`} style={{ width: `${pct >= 0 ? pct : d.status === 'DOWNLOADING' || d.status === 'FAILED' ? 100 : 0}%` }} />
                    </div>
                    <span className="status-text" title={d.status === 'FAILED' ? d.error : undefined}>{statusLabel(d, pct)}</span>
                  </div>
                </td>
                <td className="col-speed num">{p ? formatSpeed(p.speed) : '—'}</td>
                <td className="col-eta num">{p ? formatEta(p.etaSeconds) : '—'}</td>
                <td className="col-added num">{formatDate(d.createdAt)}</td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {menu && (
        <div className="context-menu" role="menu" style={{ left: menu.x, top: menu.y }} onClick={(e) => e.stopPropagation()}>
          <button role="menuitem" disabled={menu.d.status !== 'COMPLETED'} onClick={run(actions.openFile)}><ExternalLink size={14} /> Open file</button>
          <button role="menuitem" onClick={run(actions.showInFolder)}><FolderOpen size={14} /> Show in folder</button>
          <hr />
          <button role="menuitem" disabled={!canResume(menu.d.status)} onClick={run(actions.resume)}><Play size={14} /> Resume</button>
          <button role="menuitem" disabled={!canPause(menu.d.status)} onClick={run(actions.pause)}><Pause size={14} /> Pause</button>
          <button role="menuitem" disabled={menu.d.status !== 'FAILED'} onClick={run(actions.retry)}><RotateCcw size={14} /> Retry</button>
          <button role="menuitem" disabled={!canCancel(menu.d.status)} onClick={run(actions.cancel)}><Square size={14} /> Cancel</button>
          <hr />
          <button role="menuitem" onClick={run(actions.copyLink)}><Copy size={14} /> Copy link</button>
          <button role="menuitem" className="danger" disabled={!canDelete(menu.d.status)} onClick={run(actions.remove)}><Trash2 size={14} /> Remove…</button>
        </div>
      )}
    </div>
  )
}
