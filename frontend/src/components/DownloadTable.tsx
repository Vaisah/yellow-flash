import { useEffect, useState } from 'react'
import type { MouseEvent as ReactMouseEvent } from 'react'
import {
  ArrowDown,
  ArrowUp,
  Archive,
  Copy,
  ExternalLink,
  FileCheck,
  FolderOpen,
  Layers,
  Pause,
  Play,
  RotateCcw,
  Square,
  Trash2,
} from 'lucide-react'
import { categoryLabel, iconFor } from '../lib/categories'
import { formatBytes, formatDate, formatEta, percentOf, parseTime } from '../lib/format'
import { canCancel, canDelete, canPause, canResume } from '../types'
import type { Download, Progress } from '../types'

/** An archive's name (or one volume of one), as the backend extracts them. */
function isArchive(name: string): boolean {
  const n = name.toLowerCase()
  return /\.(zip|7z|rar|tar|tgz|gz|txz|xz)$/.test(n) || /\.r\d\d$/.test(n) || /\.(7z|zip)\.\d{3}$/.test(n)
}

export type SortKey = 'name' | 'size' | 'progress' | 'eta' | 'added'
export interface Sort {
  key: SortKey
  dir: 'asc' | 'desc'
}

export function statusLabel(d: Download, pct: number): string {
  switch (d.status) {
    case 'DOWNLOADING': return pct >= 0 ? `${pct.toFixed(1)}%` : 'Downloading'
    case 'INITIALIZING': return 'Connecting…'
    case 'PAUSING': return 'Pausing…'
    case 'PAUSED': return pct > 0 ? `Paused · ${pct.toFixed(0)}%` : 'Paused'
    case 'QUEUED': return 'Queued'
    case 'VERIFYING': return 'Verifying…'
    case 'COMPLETED': return 'Complete'
    case 'FAILED': return 'Failed'
    case 'CANCELLED': return 'Cancelled'
  }
}

export interface RowActions {
  resume(d: Download): void
  pause(d: Download): void
  cancel(d: Download): void
  retry(d: Download): void
  remove(d: Download): void
  openFile(d: Download): void
  showInFolder(d: Download): void
  copyLink(d: Download): void
  verifyChecksum?(d: Download): void
  /** Extract a finished archive. */
  extract?(d: Download): void
  /** Put it at the end of the main queue. */
  addToQueue?(d: Download): void
}

interface Props {
  rows: Download[]
  progress: Record<string, Progress>
  selectedId: string | null
  checkedIds: Set<string>
  onToggleCheck: (id: string) => void
  sort: Sort
  onSort: (key: SortKey) => void
  onSelect: (id: string) => void
  onActivate: (d: Download) => void
  actions: RowActions
}

const COLUMNS: { key: SortKey; label: string; className: string }[] = [
  { key: 'name', label: 'Name', className: 'col-name' },
  { key: 'size', label: 'Size', className: 'col-size' },
  { key: 'progress', label: 'Status', className: 'col-status' },
  { key: 'eta', label: 'Time Left', className: 'col-eta' },
  { key: 'added', label: 'Last Modification', className: 'col-mod' },
]

export function sortRows(
  rows: Download[],
  progress: Record<string, Progress>,
  sort: Sort,
): Download[] {
  const value = (d: Download): number | string => {
    const p = progress[d.id]
    switch (sort.key) {
      case 'name':
        return d.filename.toLowerCase()
      case 'size':
        return d.size
      case 'progress':
        return percentOf(p?.downloaded ?? d.downloaded, d.size)
      case 'eta':
        return p && p.etaSeconds >= 0 ? p.etaSeconds : Number.MAX_SAFE_INTEGER
      case 'added':
        return parseTime(d.createdAt)?.getTime() ?? 0
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

interface MenuState {
  x: number
  y: number
  d: Download
}

export function DownloadTable({
  rows,
  progress,
  selectedId,
  checkedIds,
  onToggleCheck,
  sort,
  onSort,
  onSelect,
  onActivate,
  actions,
}: Props) {
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
    setMenu({
      x: Math.min(e.clientX, window.innerWidth - 220),
      y: Math.min(e.clientY, window.innerHeight - 300),
      d,
    })
  }

  const run = (fn: (d: Download) => void) => () => {
    if (menu) fn(menu.d)
    setMenu(null)
  }

  return (
    <div className="table-wrap">
      <table className="downloads-table" aria-label="Downloads list">
        <thead>
          <tr>
            <th className="col-chk">
              <span className="th-chk-placeholder" />
            </th>
            {COLUMNS.map((c) => (
              <th
                key={c.key}
                className={c.className}
                aria-sort={
                  sort.key === c.key
                    ? sort.dir === 'asc'
                      ? 'ascending'
                      : 'descending'
                    : 'none'
                }
              >
                <button
                  type="button"
                  className="th-btn"
                  onClick={() => onSort(c.key)}
                >
                  <span>{c.label}</span>
                  <span className="sort-arrows">
                    {sort.key === c.key ? (
                      sort.dir === 'asc' ? (
                        <ArrowUp size={12} className="sort-active" />
                      ) : (
                        <ArrowDown size={12} className="sort-active" />
                      )
                    ) : (
                      <span className="sort-both">↕</span>
                    )}
                  </span>
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
            const subLabel = categoryLabel(d.filename)
            const isSelected = selectedId === d.id
            const isChecked = checkedIds.has(d.id)
            const isCompleted = d.status === 'COMPLETED'
            const isDownloading = d.status === 'DOWNLOADING' || d.status === 'INITIALIZING'
            const isPaused = d.status === 'PAUSED'
            const isFailed = d.status === 'FAILED'

            return (
              <tr
                key={d.id}
                className={`table-row ${isSelected ? 'selected' : ''}`}
                onClick={() => onSelect(d.id)}
                onDoubleClick={() => onActivate(d)}
                onContextMenu={(e) => openMenu(e, d)}
                tabIndex={isSelected ? 0 : -1}
                aria-selected={isSelected}
              >
                {/* 1. Checkbox column */}
                <td
                  className="col-chk"
                  onClick={(e) => {
                    e.stopPropagation()
                    onToggleCheck(d.id)
                  }}
                >
                  <div
                    className={`custom-checkbox-box ${isChecked ? 'checked' : ''}`}
                    aria-label={`Select ${d.filename}`}
                  >
                    {isChecked && (
                      <svg viewBox="0 0 14 14" fill="none" className="check-svg">
                        <path
                          d="M3 7.5L5.5 10L11 4.5"
                          stroke="white"
                          strokeWidth="2"
                          strokeLinecap="round"
                          strokeLinejoin="round"
                        />
                      </svg>
                    )}
                  </div>
                </td>

                {/* 2. Name & Category column */}
                <td className="col-name">
                  <div className="name-cell">
                    <div className="file-badge-square">
                      <Icon size={17} className="file-badge-icon" />
                    </div>
                    <div className="name-text-col">
                      <span className="name-title" title={d.filename}>
                        {d.filename}
                      </span>
                      <span className="name-subtitle">{subLabel}</span>
                    </div>
                  </div>
                </td>

                {/* 3. Size column */}
                <td className="col-size num-text">
                  {formatBytes(d.size)}
                </td>

                {/* 4. Status column */}
                <td className="col-status">
                  {isCompleted && (
                    <span className="status-complete-text">Complete</span>
                  )}

                  {isDownloading && (
                    <div className="status-progress-wrap">
                      <div className="status-pct-label">
                        {pct >= 0 ? `${Math.round(pct)}%` : 'Downloading'}
                      </div>
                      <div className="progress-track">
                        <div
                          className="progress-fill"
                          style={{
                            width: `${pct >= 0 ? pct : 35}%`,
                          }}
                        />
                      </div>
                    </div>
                  )}

                  {isPaused && (
                    <div className="status-paused-box">
                      <Square size={13} className="pause-square-icon" />
                      <span className="status-paused-text">Paused</span>
                    </div>
                  )}

                  {pct === 0 && !isCompleted && !isDownloading && !isPaused && !isFailed && (
                    <span className="status-zero-text">0%</span>
                  )}

                  {isFailed && (
                    <span className="status-failed-text" title={d.error || 'Download failed'}>
                      Failed
                    </span>
                  )}
                </td>

                {/* 5. Time Left column */}
                <td className="col-eta num-text">
                  {isCompleted ? '0 Sec' : formatEta(p?.etaSeconds ?? (isDownloading ? 0 : -1))}
                </td>

                {/* 6. Last Modification column */}
                <td className="col-mod num-text">
                  {formatDate(d.createdAt)}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>

      {/* Context Menu */}
      {menu && (
        <div
          className="context-menu"
          role="menu"
          style={{ left: menu.x, top: menu.y }}
          onClick={(e) => e.stopPropagation()}
        >
          <button
            type="button"
            role="menuitem"
            disabled={menu.d.status !== 'COMPLETED'}
            onClick={run(actions.openFile)}
          >
            <ExternalLink size={14} /> Open file
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={run(actions.showInFolder)}
          >
            <FolderOpen size={14} /> Show in folder
          </button>
          <button
            type="button"
            role="menuitem"
            onClick={run((d) => actions.verifyChecksum?.(d))}
          >
            <FileCheck size={14} /> Verify checksum (MD5/SHA256)...
          </button>
          {actions.extract && isArchive(menu.d.filename) && (
            <button
              type="button"
              role="menuitem"
              disabled={menu.d.status !== 'COMPLETED'}
              onClick={run((d) => actions.extract?.(d))}
            >
              <Archive size={14} /> Extract archive
            </button>
          )}
          {actions.addToQueue && (
            <button
              type="button"
              role="menuitem"
              disabled={menu.d.status === 'COMPLETED' || !!menu.d.queueId}
              onClick={run((d) => actions.addToQueue?.(d))}
            >
              <Layers size={14} /> Add to Main Queue
            </button>
          )}
          <hr />
          <button
            type="button"
            role="menuitem"
            disabled={!canResume(menu.d.status)}
            onClick={run(actions.resume)}
          >
            <Play size={14} /> Resume
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!canPause(menu.d.status)}
            onClick={run(actions.pause)}
          >
            <Pause size={14} /> Stop
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={menu.d.status !== 'FAILED'}
            onClick={run(actions.retry)}
          >
            <RotateCcw size={14} /> Retry
          </button>
          <button
            type="button"
            role="menuitem"
            disabled={!canCancel(menu.d.status)}
            onClick={run(actions.cancel)}
          >
            <Square size={14} /> Cancel
          </button>
          <hr />
          <button
            type="button"
            role="menuitem"
            onClick={run(actions.copyLink)}
          >
            <Copy size={14} /> Copy link
          </button>
          <button
            type="button"
            role="menuitem"
            className="danger"
            disabled={!canDelete(menu.d.status)}
            onClick={run(actions.remove)}
          >
            <Trash2 size={14} /> Delete
          </button>
        </div>
      )}
    </div>
  )
}
