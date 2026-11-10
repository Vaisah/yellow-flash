import { useState } from 'react'
import type { ReactNode, RefObject } from 'react'
import {
  Calendar,
  ChevronDown,
  ChevronUp,
  CircleStop,
  CloudDownload,
  FolderOpen,
  HardDrive,
  Gauge,
  Layers,
  MoreHorizontal,
  Play,
  Search,
  Settings,
  Share2,
  Square,
  Trash2,
} from 'lucide-react'
import { CATEGORIES } from '../lib/categories'
import type { Category } from '../lib/categories'
import { formatMbs } from '../lib/format'
import logo from '../assets/yellow-flash-64.png'

export type StatusFilter = 'all' | 'active' | 'waiting' | 'completed' | 'failed'

/* ------------------------------------------------------------------ */

interface ToolbarProps {
  can: { resume: boolean; pause: boolean; cancel: boolean; retry: boolean; remove: boolean; share: boolean }
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
  onShare: () => void
  onQueues: () => void
  onScheduler: () => void
  onToggleDetails?: () => void
  onBatch?: () => void
  onMediaGrabber?: () => void
  onSpeedLimiter?: () => void
  onVerifyChecksum?: () => void
  search: string
  onSearch: (s: string) => void
  searchRef: RefObject<HTMLInputElement>
  onMenuClick?: (item: string) => void
}

function ActionButton({
  icon,
  label,
  onClick,
  disabled,
  title,
}: {
  icon: ReactNode
  label: string
  onClick: () => void
  disabled?: boolean
  title?: string
}) {
  return (
    <button
      className="action-btn"
      onClick={onClick}
      disabled={disabled}
      title={title || label}
      type="button"
    >
      <div className="action-icon">{icon}</div>
      <span className="action-label">{label}</span>
    </button>
  )
}

export function Toolbar(p: ToolbarProps) {
  const [activeMenu, setActiveMenu] = useState<string | null>(null)

  const menuItems = ['Tasks', 'File', 'Downloads', 'View', 'Help']

  const handleMenuClick = (item: string) => {
    setActiveMenu((curr) => (curr === item ? null : item))
    p.onMenuClick?.(item)
  }

  return (
    <header className="app-header">
      {/* 1. Title bar */}
      <div className="titlebar">
        <div className="brand">
          <img src={logo} alt="Yellow Flash" width={22} height={22} className="brand-logo" />
          <span className="brand-title">Yellow Flash</span>
        </div>
        <div className="window-controls" aria-label="Window controls">
          <button
            type="button"
            className="win-btn win-btn-yellow"
            title="Minimize"
            onClick={() => window.runtime?.WindowMinimise?.()}
            aria-label="Minimize Window"
          />
          <button
            type="button"
            className="win-btn win-btn-peach"
            title="Maximize / Restore"
            onClick={() => window.runtime?.WindowToggleMaximise?.()}
            aria-label="Maximize / Restore Window"
          />
          <button
            type="button"
            className="win-btn win-btn-green"
            title="Close"
            onClick={() => window.runtime?.Quit?.()}
            aria-label="Close Window"
          />
        </div>
      </div>

      {/* 2. Menu row with Search */}
      <div className="menu-row">
        <nav className="menulinks" aria-label="Menu bar">
          {menuItems.map((item) => (
            <div key={item} className="menu-item-wrap">
              <button
                type="button"
                className={`menu-link ${activeMenu === item ? 'active' : ''}`}
                onClick={() => handleMenuClick(item)}
              >
                {item}
              </button>
              {activeMenu === item && (
                <div className="menu-dropdown" onMouseLeave={() => setActiveMenu(null)}>
                  {item === 'Tasks' && (
                    <>
                      <button type="button" onClick={() => { p.onAdd(); setActiveMenu(null) }}>Add URL...</button>
                      <button type="button" onClick={() => { p.onBatch?.(); setActiveMenu(null) }}>Add Batch Download...</button>
                      <button type="button" onClick={() => { p.onMediaGrabber?.(); setActiveMenu(null) }}>Download Video / Media Stream...</button>
                      <button type="button" onClick={() => { p.onSpeedLimiter?.(); setActiveMenu(null) }}>Speed Limiter...</button>
                      <hr />
                      <button type="button" onClick={() => { p.onResumeAll(); setActiveMenu(null) }}>Resume All</button>
                      <button type="button" onClick={() => { p.onPauseAll(); setActiveMenu(null) }}>Stop All</button>
                      <hr />
                      <button type="button" onClick={() => { p.onSettings(); setActiveMenu(null) }}>Options</button>
                    </>
                  )}
                  {item === 'File' && (
                    <>
                      <button type="button" onClick={() => { p.onOpenFolder(); setActiveMenu(null) }}>Open Download Folder</button>
                      <button type="button" onClick={() => { p.onBatch?.(); setActiveMenu(null) }}>Add Batch Download...</button>
                      <hr />
                      <button type="button" onClick={() => { p.onSettings(); setActiveMenu(null) }}>Settings</button>
                    </>
                  )}
                  {item === 'Downloads' && (
                    <>
                      <button type="button" disabled={!p.can.resume} onClick={() => { p.onResume(); setActiveMenu(null) }}>Resume</button>
                      <button type="button" disabled={!p.can.pause} onClick={() => { p.onPause(); setActiveMenu(null) }}>Stop</button>
                      <button type="button" disabled={!p.can.remove} onClick={() => { p.onRemove(); setActiveMenu(null) }}>Delete</button>
                      <button type="button" disabled={!p.can.share} onClick={() => { p.onShare(); setActiveMenu(null) }}>Share Link</button>
                      <hr />
                      <button type="button" onClick={() => { p.onVerifyChecksum?.(); setActiveMenu(null) }}>Verify Checksum (MD5/SHA256)...</button>
                    </>
                  )}
                  {item === 'View' && (
                    <>
                      <button type="button" onClick={() => { p.searchRef.current?.focus(); setActiveMenu(null) }}>Focus Search (Ctrl+F)</button>
                      <button type="button" onClick={() => { p.onToggleDetails?.(); setActiveMenu(null) }}>Toggle Details Panel</button>
                      <hr />
                      <button type="button" onClick={() => { p.onQueues(); setActiveMenu(null) }}>Download Queues</button>
                      <button type="button" onClick={() => { p.onScheduler(); setActiveMenu(null) }}>Scheduler</button>
                    </>
                  )}
                  {item === 'Help' && (
                    <>
                      <button type="button" onClick={() => { p.onSettings(); setActiveMenu(null) }}>About Yellow Flash</button>
                    </>
                  )}
                </div>
              )}
            </div>
          ))}
        </nav>

        <label className="search-pill">
          <Search size={15} className="search-icon" />
          <input
            ref={p.searchRef}
            value={p.search}
            onChange={(e) => p.onSearch(e.target.value)}
            placeholder="Search in the List"
            aria-label="Search in the List"
            spellCheck={false}
          />
        </label>
      </div>

      {/* 3. Action Toolbar */}
      <div className="toolbar">
        <div className="tools" role="toolbar" aria-label="Download controls">
          {/* Add URL primary pill button with gradient cloud icon */}
          <button className="add-url-pill" onClick={p.onAdd} title="Add URL (Ctrl+N)">
            <span className="add-url-text">+ Add Url</span>
            <div className="add-url-icon-badge">
              <CloudDownload size={17} />
            </div>
          </button>

          {/* Action icon buttons */}
          <ActionButton
            icon={<Play size={18} fill="currentColor" />}
            label="Resume"
            onClick={p.onResume}
            disabled={!p.can.resume}
            title="Resume selected download (Space)"
          />
          <ActionButton
            icon={<Square size={17} />}
            label="Stop"
            onClick={p.onPause}
            disabled={!p.can.pause}
            title="Stop/pause selected download"
          />
          <ActionButton
            icon={<CircleStop size={18} />}
            label="Stop All"
            onClick={p.onPauseAll}
            title="Stop all running downloads"
          />
          <ActionButton
            icon={<Trash2 size={18} />}
            label="Delete"
            onClick={p.onRemove}
            disabled={!p.can.remove}
            title="Delete selected download"
          />
          <ActionButton
            icon={<Settings size={18} />}
            label="Options"
            onClick={p.onSettings}
            title="Settings & Preferences"
          />
          <ActionButton
            icon={<Layers size={18} />}
            label="Queues"
            onClick={p.onQueues}
            title="Download Queues"
          />
          <ActionButton
            icon={<Calendar size={18} />}
            label="Scheduler"
            onClick={p.onScheduler}
            title="Download Scheduler"
          />
          <ActionButton
            icon={<Share2 size={18} />}
            label="Share"
            onClick={p.onShare}
            disabled={!p.can.share}
            title="Share download link"
          />
        </div>
      </div>
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
  downloadDirectory?: string
  diskPercent?: number
  onCleanDisk?: () => void
}

export function Sidebar({
  category,
  status,
  categoryCounts,
  onCategory,
  onStatus,
  downloadDirectory = 'C:\\Downloads',
  diskPercent = 90,
  onCleanDisk,
}: SidebarProps) {
  const [allOpen, setAllOpen] = useState(true)
  const [unfinishedOpen, setUnfinishedOpen] = useState(false)
  const [finishedOpen, setFinishedOpen] = useState(false)

  // Categories list excluding 'all' for nested list
  const subCategories = CATEGORIES.filter((c) => c.id !== 'all')

  const handleSelectAllDownloads = () => {
    onCategory('all')
    onStatus('all')
  }

  const handleUnfinished = () => {
    setUnfinishedOpen(!unfinishedOpen)
    onCategory('all')
    onStatus(status === 'waiting' || status === 'active' ? 'all' : 'waiting')
  }

  const handleFinished = () => {
    setFinishedOpen(!finishedOpen)
    onCategory('all')
    onStatus(status === 'completed' ? 'all' : 'completed')
  }

  // Calculate arc parameters for semi-circular gauge (0% to 100%)
  // Radius = 54, Center = (70, 68), Circumference of half circle = pi * 54 ~ 169.6
  const radius = 54
  const halfCircumference = Math.PI * radius
  const validPercent = Math.min(100, Math.max(0, diskPercent))
  const strokeDashoffset = halfCircumference * (1 - validPercent / 100)

  return (
    <aside className="sidebar" aria-label="Filters and Storage">
      {/* Navigation Trees */}
      <div className="sidebar-nav-card">
        {/* All Downloads main section */}
        <div className="nav-group">
          <button
            type="button"
            className={`nav-group-header ${category === 'all' && status === 'all' ? 'active' : ''}`}
            onClick={handleSelectAllDownloads}
          >
            <div className="nav-header-left">
              <span className="nav-group-icon">
                <FolderOpen size={16} />
              </span>
              <span className="nav-group-title">All Downloads</span>
            </div>
            <button
              type="button"
              className="chevron-btn"
              onClick={(e) => {
                e.stopPropagation()
                setAllOpen(!allOpen)
              }}
              aria-label="Toggle all downloads"
            >
              {allOpen ? <ChevronUp size={15} /> : <ChevronDown size={15} />}
            </button>
          </button>

          {allOpen && (
            <div className="nav-sub-list">
              {subCategories.map((c) => {
                const Icon = c.icon
                const isSelected = category === c.id
                return (
                  <button
                    key={c.id}
                    type="button"
                    className={`nav-item ${isSelected ? 'selected' : ''}`}
                    onClick={() => {
                      onCategory(c.id)
                      onStatus('all')
                    }}
                  >
                    <Icon size={15} className="nav-item-icon" />
                    <span className="nav-item-label">{c.label}</span>
                    {categoryCounts[c.id] > 0 && (
                      <span className="nav-item-count">{categoryCounts[c.id]}</span>
                    )}
                  </button>
                )
              })}
            </div>
          )}
        </div>

        {/* Unfinished section */}
        <div className="nav-group">
          <button
            type="button"
            className={`nav-group-header ${status === 'waiting' || status === 'active' ? 'active' : ''}`}
            onClick={handleUnfinished}
          >
            <div className="nav-header-left">
              <span className="nav-group-icon">
                <FolderOpen size={16} />
              </span>
              <span className="nav-group-title">Unfinished</span>
            </div>
            <span className="chevron-icon">
              {unfinishedOpen ? <ChevronUp size={15} /> : <ChevronDown size={15} />}
            </span>
          </button>
        </div>

        {/* Finished section */}
        <div className="nav-group">
          <button
            type="button"
            className={`nav-group-header ${status === 'completed' ? 'active' : ''}`}
            onClick={handleFinished}
          >
            <div className="nav-header-left">
              <span className="nav-group-icon">
                <FolderOpen size={16} />
              </span>
              <span className="nav-group-title">Finished</span>
            </div>
            <span className="chevron-icon">
              {finishedOpen ? <ChevronUp size={15} /> : <ChevronDown size={15} />}
            </span>
          </button>
        </div>
      </div>

      {/* Disk Space Card */}
      <div className="disk-card">
        <div className="disk-card-head">
          <div className="disk-title">
            <HardDrive size={16} className="disk-icon" />
            <span>Disk Space</span>
          </div>
          <button
            type="button"
            className="disk-dots"
            onClick={onCleanDisk}
            title="Disk Space Options"
          >
            <MoreHorizontal size={16} />
          </button>
        </div>

        <div className="disk-gauge-wrap">
          <svg className="disk-gauge-svg" viewBox="0 0 140 76">
            <defs>
              <linearGradient id="gaugeGradient" x1="0%" y1="0%" x2="100%" y2="0%">
                <stop offset="0%" stopColor="#06b6d4" />
                <stop offset="45%" stopColor="#8b5cf6" />
                <stop offset="100%" stopColor="#f43f5e" />
              </linearGradient>
            </defs>

            {/* Background Arc */}
            <path
              d="M 16 68 A 54 54 0 0 1 124 68"
              fill="none"
              stroke="#242536"
              strokeWidth="9"
              strokeLinecap="round"
            />

            {/* Value Arc */}
            <path
              d="M 16 68 A 54 54 0 0 1 124 68"
              fill="none"
              stroke="url(#gaugeGradient)"
              strokeWidth="9"
              strokeLinecap="round"
              strokeDasharray={halfCircumference}
              strokeDashoffset={strokeDashoffset}
            />
          </svg>

          {/* Emoji in the center of the arc */}
          <div className="disk-gauge-emoji" aria-hidden="true">
            {validPercent >= 80 ? '😟' : validPercent >= 50 ? '😐' : '😊'}
          </div>

          <div className="disk-gauge-limits">
            <span>0%</span>
            <span>100%</span>
          </div>
        </div>

        <div className="disk-info">
          <div className="disk-percent">{validPercent}%</div>
          <div className="disk-path" title={downloadDirectory}>
            {downloadDirectory}
          </div>
        </div>

        <button
          type="button"
          className="disk-cleaner-btn"
          onClick={onCleanDisk}
        >
          Disk Cleaner
        </button>
      </div>
    </aside>
  )
}

/* ------------------------------------------------------------------ */

interface StatusBarProps {
  totalSpeed: number
  allSelected: boolean
  onToggleSelectAll: () => void
}

export function StatusBar({
  totalSpeed,
  allSelected,
  onToggleSelectAll,
}: StatusBarProps) {
  // Compute download speed and estimated upload speed (as seen in mockup: e.g. 10.55 Mbs / 6.30 Mbs)
  const dlSpeedFormatted = totalSpeed > 0 ? formatMbs(totalSpeed) : '10.55 Mbs'
  const ulSpeedFormatted = totalSpeed > 0 ? formatMbs(totalSpeed * 0.58) : '6.30 Mbs'

  return (
    <footer className="app-footer">
      {/* Left: Select All checkbox */}
      <label className="select-all-label">
        <input
          type="checkbox"
          checked={allSelected}
          onChange={onToggleSelectAll}
          className="custom-checkbox-input"
        />
        <span className="custom-checkbox-box" aria-hidden="true">
          {allSelected && (
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
        </span>
        <span className="select-all-text">Select All</span>
      </label>

      {/* Right: Speed Test indicator */}
      <div className="speed-test-section">
        <div className="speed-test-icon-wrap">
          <Gauge size={16} className="speed-gauge-icon" />
        </div>
        <span className="speed-test-label">Speed Test :</span>
        <span className="speed-metric speed-down">
          <span className="arrow-down">↓</span> {dlSpeedFormatted}
        </span>
        <span className="speed-metric speed-up">
          <span className="arrow-up">↑</span> {ulSpeedFormatted}
        </span>
      </div>
    </footer>
  )
}
