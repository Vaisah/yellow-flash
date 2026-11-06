import type { ReactNode, RefObject } from 'react'
import { CheckCircle2, CircleAlert, Clock, Download as DownloadIcon, FolderOpen, Pause, Play, Plus, RotateCcw, Search, Settings, Square, Trash2, PauseCircle, PlayCircle } from 'lucide-react'
import { CATEGORIES } from '../lib/categories'
import type { Category } from '../lib/categories'
import { formatSpeed } from '../lib/format'
import logo from '../assets/yellow-flash-64.png'

export type StatusFilter = 'all' | 'active' | 'waiting' | 'completed' | 'failed'

/* ------------------------------------------------------------------ */

interface ToolbarProps {
  can: { resume: boolean; pause: boolean; cancel: boolean; retry: boolean; remove: boolean }
  onAdd: () => void
  onResume: () => void
  onPause: () => void
  onCancel: () => void
  onRetry: () => void
  onRemove: () => void
  onResumeAll: () => void
  onPauseAll: () => void
  onOpenFolder: () => void
  onSettings: () => void
  search: string
  onSearch: (s: string) => void
  searchRef: RefObject<HTMLInputElement>
}

function ToolButton({ icon, label, onClick, disabled, shortcut }: { icon: ReactNode; label: string; onClick: () => void; disabled?: boolean; shortcut?: string }) {
  return (
    <button className="tool" onClick={onClick} disabled={disabled} title={shortcut ? `${label} (${shortcut})` : label}>
      {icon}
      <span>{label}</span>
    </button>
  )
}

export function Toolbar(p: ToolbarProps) {
  return (
    <header className="toolbar">
      <div className="brand">
        <img src={logo} alt="" width={36} height={36} />
        <div>
          <strong>Yellow Flash</strong>
          <span>Download Manager</span>
        </div>
      </div>
      <div className="tools" role="toolbar" aria-label="Download actions">
        <button className="tool tool-primary" onClick={p.onAdd} title="Add URL (Ctrl+N)">
          <Plus size={20} />
          <span>Add URL</span>
        </button>
        <span className="tool-sep" />
        <ToolButton icon={<Play size={18} />} label="Resume" onClick={p.onResume} disabled={!p.can.resume} shortcut="Space" />
        <ToolButton icon={<Pause size={18} />} label="Pause" onClick={p.onPause} disabled={!p.can.pause} shortcut="Space" />
        <ToolButton icon={<Square size={17} />} label="Cancel" onClick={p.onCancel} disabled={!p.can.cancel} />
        <ToolButton icon={<RotateCcw size={18} />} label="Retry" onClick={p.onRetry} disabled={!p.can.retry} />
        <ToolButton icon={<Trash2 size={18} />} label="Remove" onClick={p.onRemove} disabled={!p.can.remove} shortcut="Del" />
        <span className="tool-sep" />
        <ToolButton icon={<PlayCircle size={18} />} label="Resume all" onClick={p.onResumeAll} />
        <ToolButton icon={<PauseCircle size={18} />} label="Pause all" onClick={p.onPauseAll} />
        <span className="tool-sep" />
        <ToolButton icon={<FolderOpen size={18} />} label="Folder" onClick={p.onOpenFolder} />
        <ToolButton icon={<Settings size={18} />} label="Settings" onClick={p.onSettings} />
      </div>
      <label className="search">
        <Search size={15} />
        <input ref={p.searchRef} value={p.search} onChange={(e) => p.onSearch(e.target.value)} placeholder="Search downloads" aria-label="Search downloads" />
        <kbd>Ctrl F</kbd>
      </label>
    </header>
  )
}

/* ------------------------------------------------------------------ */

interface SidebarProps {
  category: Category
  status: StatusFilter
  categoryCounts: Record<Category, number>
  statusCounts: Record<StatusFilter, number>
  onCategory: (c: Category) => void
  onStatus: (s: StatusFilter) => void
}

const STATUS_FILTERS: { id: StatusFilter; label: string; icon: ReactNode }[] = [
  { id: 'all', label: 'Everything', icon: <DownloadIcon size={16} /> },
  { id: 'active', label: 'Downloading', icon: <Play size={16} /> },
  { id: 'waiting', label: 'Unfinished', icon: <Clock size={16} /> },
  { id: 'completed', label: 'Finished', icon: <CheckCircle2 size={16} /> },
  { id: 'failed', label: 'Failed', icon: <CircleAlert size={16} /> },
]

export function Sidebar({ category, status, categoryCounts, statusCounts, onCategory, onStatus }: SidebarProps) {
  return (
    <nav className="sidebar" aria-label="Filters">
      <p className="nav-caption">Status</p>
      {STATUS_FILTERS.map((f) => (
        <button key={f.id} className={`nav ${status === f.id ? 'on' : ''}`} aria-pressed={status === f.id} onClick={() => onStatus(f.id)}>
          {f.icon}
          <span>{f.label}</span>
          <small>{statusCounts[f.id]}</small>
        </button>
      ))}
      <p className="nav-caption">Categories</p>
      {CATEGORIES.map((c) => (
        <button key={c.id} className={`nav ${category === c.id ? 'on' : ''}`} aria-pressed={category === c.id} onClick={() => onCategory(c.id)}>
          <c.icon size={16} />
          <span>{c.label}</span>
          <small>{categoryCounts[c.id]}</small>
        </button>
      ))}
    </nav>
  )
}

/* ------------------------------------------------------------------ */

interface StatusBarProps {
  totalSpeed: number
  active: number
  waiting: number
  completed: number
  total: number
  connected: boolean
}

export function StatusBar({ totalSpeed, active, waiting, completed, total, connected }: StatusBarProps) {
  return (
    <footer className="statusbar">
      <span className={`engine ${connected ? 'ok' : 'down'}`}><i />{connected ? 'Engine ready' : 'Engine unavailable'}</span>
      <span>{active} downloading</span>
      <span>{waiting} waiting</span>
      <span>{completed} finished</span>
      <span>{total} total</span>
      <span className="speed">⚡ {formatSpeed(totalSpeed)}</span>
    </footer>
  )
}
