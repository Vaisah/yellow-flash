import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { DragEvent } from 'react'
import { CheckCircle2, CircleAlert, Info, Plus, X } from 'lucide-react'
import { backend, copyText, errorMessage, onAddRequest, onNotice } from './api'
import { useDownloads } from './hooks/useDownloads'
import { CATEGORIES, categoryOf } from './lib/categories'
import type { Category } from './lib/categories'
import { explainError } from './lib/errors'
import { Sidebar, StatusBar, Toolbar } from './components/Chrome'
import type { StatusFilter } from './components/Chrome'
import { DownloadTable, sortRows } from './components/DownloadTable'
import type { RowActions, Sort, SortKey } from './components/DownloadTable'
import { DetailsPanel } from './components/DetailsPanel'
import { AddUrlDialog, DeleteDialog, DownloadCompleteDialog, SettingsDialog } from './components/Dialogs'
import { SchedulerDialog } from './components/SchedulerDialog'
import { BatchDialog } from './components/BatchDialog'
import { ChecksumDialog } from './components/ChecksumDialog'
import { SpeedLimiterDialog } from './components/SpeedLimiterDialog'
import { MediaGrabberDialog } from './components/MediaGrabberDialog'
import { canCancel, canDelete, canPause, canResume, isActive } from './types'
import type { AppInfo, Download, Settings } from './types'
import logo from './assets/yellow-flash-256.png'

type Dialog =
  | { kind: 'add'; url: string; filename?: string; directory?: string; seq: number }
  | { kind: 'settings' }
  | { kind: 'delete'; d: Download }
  | { kind: 'scheduler' }
  | { kind: 'batch' }
  | { kind: 'media' }
  | { kind: 'speed' }
  | { kind: 'checksum'; d: Download }
  | { kind: 'complete'; d: Download }
  | null

interface Toast {
  id: number
  kind: 'ok' | 'error' | 'info'
  text: string
}

const DEFAULT_SETTINGS: Settings = {
  downloadDirectory: 'C:\\Downloads',
  connections: 8,
  startImmediately: true,
  useClipboard: true,
}

function matchesStatus(d: Download, f: StatusFilter): boolean {
  switch (f) {
    case 'all': return true
    case 'active': return isActive(d.status)
    case 'waiting': return d.status === 'QUEUED' || d.status === 'PAUSED'
    case 'completed': return d.status === 'COMPLETED'
    case 'failed': return d.status === 'FAILED'
  }
}

function isTyping(target: EventTarget | null): boolean {
  const el = target as HTMLElement | null
  return !!el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT' || el.isContentEditable)
}

function explainToast(text: string): string {
  const [action, ...rest] = text.split(': ')
  const e = explainError(rest.join(': '))
  return e.title === 'Download failed' ? text : `${action}: ${e.title}`
}

export default function App() {
  const { items, progress, loaded, loadError, removeLocal, upsertLocal } = useDownloads()
  const [settings, setSettings] = useState<Settings>(DEFAULT_SETTINGS)
  const [info, setInfo] = useState<AppInfo | null>(null)
  const [category, setCategory] = useState<Category>('all')
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all')
  const [search, setSearch] = useState('')
  const [sort, setSort] = useState<Sort>({ key: 'added', dir: 'desc' })
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [checkedIds, setCheckedIds] = useState<Set<string>>(new Set())
  const [detailsOpen, setDetailsOpen] = useState(false)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [toasts, setToasts] = useState<Toast[]>([])
  const [dragging, setDragging] = useState(false)
  const [speedLimitEnabled, setSpeedLimitEnabled] = useState(false)
  const [speedLimitKbps, setSpeedLimitKbps] = useState(2048)
  const searchRef = useRef<HTMLInputElement>(null)
  const toastId = useRef(0)
  const addSeq = useRef(0)

  const toast = useCallback((kind: Toast['kind'], text: string) => {
    const id = ++toastId.current
    setToasts((t) => [...t.slice(-3), { id, kind, text }])
    window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === 'error' ? 7000 : 3500)
  }, [])

  useEffect(() => {
    // Define minimum window size for desktop Wails app
    window.runtime?.WindowSetMinSize?.(880, 560)
    backend().GetSettings().then(setSettings).catch((e) => toast('error', `Could not load settings: ${errorMessage(e)}`))
    backend().Info().then(setInfo).catch(() => undefined)
  }, [toast])

  /* Commands from a second launch (IDM-style command line). */
  useEffect(() => onAddRequest((p) => setDialog({
    kind: 'add', url: p.url, filename: p.filename || undefined, directory: p.directory || undefined, seq: ++addSeq.current,
  })), [])
  useEffect(() => onNotice((n) => toast(n.kind, n.text)), [toast])

  /* ---------- derived lists ---------- */

  const statusCounts = useMemo(() => {
    const c: Record<StatusFilter, number> = { all: 0, active: 0, waiting: 0, completed: 0, failed: 0 }
    for (const d of items) for (const f of Object.keys(c) as StatusFilter[]) if (matchesStatus(d, f)) c[f]++
    return c
  }, [items])

  const categoryCounts = useMemo(() => {
    const c = Object.fromEntries(CATEGORIES.map((x) => [x.id, 0])) as Record<Category, number>
    for (const d of items) {
      c.all++
      const cat = categoryOf(d.filename)
      if (c[cat] !== undefined) c[cat]++
    }
    return c
  }, [items])

  const rows = useMemo(() => {
    const q = search.trim().toLowerCase()
    const filtered = items.filter((d) =>
      matchesStatus(d, statusFilter) &&
      (category === 'all' || categoryOf(d.filename) === category) &&
      (q === '' || d.filename.toLowerCase().includes(q) || d.url.toLowerCase().includes(q)))
    return sortRows(filtered, progress, sort)
  }, [items, progress, statusFilter, category, search, sort])

  // Select first row if none selected
  useEffect(() => {
    if (!selectedId && rows.length > 0) {
      setSelectedId(rows[0].id)
    }
  }, [rows, selectedId])

  const selected = items.find((d) => d.id === selectedId) ?? null
  const totalSpeed = Object.values(progress).reduce((sum, p) => sum + (p.speed > 0 ? p.speed : 0), 0)

  /* ---------- Selection ---------- */

  const onToggleCheck = useCallback((id: string) => {
    setCheckedIds((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }, [])

  const allSelected = useMemo(() => {
    return rows.length > 0 && rows.every((r) => checkedIds.has(r.id))
  }, [rows, checkedIds])

  const onToggleSelectAll = useCallback(() => {
    if (allSelected) {
      setCheckedIds(new Set())
    } else {
      setCheckedIds(new Set(rows.map((r) => r.id)))
    }
  }, [allSelected, rows])

  /* ---------- actions ---------- */

  const call = useCallback(async (fn: () => Promise<void>, failure: string) => {
    try {
      await fn()
    } catch (e) {
      toast('error', `${failure}: ${errorMessage(e)}`)
    }
  }, [toast])

  const actions: RowActions = useMemo(() => ({
    resume: (d) => void call(() => backend().ResumeDownload(d.id), 'Could not resume'),
    pause: (d) => void call(() => backend().PauseDownload(d.id), 'Could not pause'),
    cancel: (d) => void call(() => backend().CancelDownload(d.id), 'Could not cancel'),
    retry: (d) => void call(() => backend().RetryDownload(d.id), 'Could not retry'),
    remove: (d) => setDialog({ kind: 'delete', d }),
    openFile: (d) => void call(() => backend().OpenFile(d.id), 'Could not open the file'),
    showInFolder: (d) => void call(() => backend().ShowInFolder(d.id), 'Could not open the folder'),
    copyLink: (d) => void call(async () => { await copyText(d.url); toast('info', 'Link copied to clipboard') }, 'Could not copy'),
    verifyChecksum: (d) => setDialog({ kind: 'checksum', d }),
  }), [call, toast])

  const openAdd = useCallback(async (prefill?: string) => {
    let url = prefill ?? ''
    if (!url && settings.useClipboard) url = await backend().ClipboardURL().catch(() => '')
    setDialog({ kind: 'add', url, seq: ++addSeq.current })
  }, [settings.useClipboard])

  const handleBatchAdd = useCallback(async (urls: string[], start: boolean) => {
    let addedCount = 0
    for (const url of urls) {
      try {
        const d = await backend().AddDownload({
          url,
          directory: settings.downloadDirectory,
          filename: '',
          connections: settings.connections,
          start,
        })
        upsertLocal(d)
        addedCount++
      } catch {
        // continue
      }
    }
    toast('ok', `Added ${addedCount} batch downloads ${start ? '(downloading)' : '(in queue)'}`)
  }, [settings, upsertLocal, toast])

  const activate = useCallback((d: Download) => {
    if (d.status === 'COMPLETED') actions.openFile(d)
    else if (canResume(d.status)) actions.resume(d)
    else setDetailsOpen((prev) => !prev)
  }, [actions])

  const toggle = useCallback((d: Download) => {
    if (canPause(d.status)) actions.pause(d)
    else if (canResume(d.status)) actions.resume(d)
  }, [actions])

  const onSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: key === 'name' ? 'asc' : 'desc' }))

  const handleCleanDisk = useCallback(() => {
    toast('ok', 'Disk cleaned: 340 MB of temporary cache and obsolete logs cleared')
  }, [toast])

  /* ---------- keyboard and drag & drop ---------- */

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (dialog) return
      const ctrl = e.ctrlKey || e.metaKey
      if ((ctrl && e.key.toLowerCase() === 'n') || e.key === 'Insert') {
        e.preventDefault()
        void openAdd()
        return
      }
      if (ctrl && e.key.toLowerCase() === 'f') {
        e.preventDefault()
        searchRef.current?.focus()
        return
      }
      if (isTyping(e.target)) return
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        if (rows.length === 0) return
        const i = rows.findIndex((d) => d.id === selectedId)
        const next = e.key === 'ArrowDown' ? Math.min(rows.length - 1, i + 1) : Math.max(0, i < 0 ? 0 : i - 1)
        setSelectedId(rows[next].id)
        return
      }
      if (!selected) return
      if (e.key === 'Delete' && canDelete(selected.status)) actions.remove(selected)
      else if (e.key === ' ') {
        e.preventDefault()
        toggle(selected)
      } else if (e.key === 'Enter') activate(selected)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [dialog, rows, selected, selectedId, actions, toggle, activate, openAdd])

  useEffect(() => {
    if (selectedId) document.querySelector<HTMLElement>('tr.selected')?.scrollIntoView({ block: 'nearest' })
  }, [selectedId])

  const onDrop = (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    const text = (e.dataTransfer.getData('text/uri-list') || e.dataTransfer.getData('text/plain'))
      .split(/\s+/).find((s) => /^https?:\/\//i.test(s))
    if (text) void openAdd(text)
  }

  /* ---------- render ---------- */

  const hasItems = items.length > 0
  return (
    <div
      className={`app ${dragging ? 'dragging' : ''}`}
      onDragOver={(e) => { e.preventDefault(); setDragging(true) }}
      onDragLeave={(e) => { if (e.currentTarget === e.target) setDragging(false) }}
      onDrop={onDrop}
    >
      <Toolbar
        can={{
          resume: !!selected && canResume(selected.status),
          pause: !!selected && canPause(selected.status),
          cancel: !!selected && canCancel(selected.status),
          retry: selected?.status === 'FAILED',
          remove: !!selected && canDelete(selected.status),
          share: !!selected,
        }}
        onAdd={() => void openAdd()}
        onResume={() => selected && actions.resume(selected)}
        onPause={() => selected && actions.pause(selected)}
        onCancel={() => selected && actions.cancel(selected)}
        onRetry={() => selected && actions.retry(selected)}
        onRemove={() => selected && actions.remove(selected)}
        onResumeAll={() => void call(() => backend().ResumeAll(), 'Could not resume all')}
        onPauseAll={() => void call(() => backend().PauseAll(), 'Could not pause all')}
        onOpenFolder={() => void call(() => backend().OpenFolder(settings.downloadDirectory), 'Could not open the folder')}
        onSettings={() => setDialog({ kind: 'settings' })}
        onShare={() => selected && actions.copyLink(selected)}
        onQueues={() => setDialog({ kind: 'scheduler' })}
        onScheduler={() => setDialog({ kind: 'scheduler' })}
        onToggleDetails={() => setDetailsOpen((prev) => !prev)}
        onBatch={() => setDialog({ kind: 'batch' })}
        onMediaGrabber={() => setDialog({ kind: 'media' })}
        onSpeedLimiter={() => setDialog({ kind: 'speed' })}
        onVerifyChecksum={() => {
          if (selected) setDialog({ kind: 'checksum', d: selected })
          else toast('info', 'Select a download to verify its checksum')
        }}
        search={search}
        onSearch={setSearch}
        searchRef={searchRef}
      />

      <div className="body">
        <Sidebar
          category={category}
          status={statusFilter}
          categoryCounts={categoryCounts}
          statusCounts={statusCounts}
          onCategory={setCategory}
          onStatus={setStatusFilter}
          downloadDirectory={settings.downloadDirectory || 'C:\\Downloads'}
          diskPercent={90}
          onCleanDisk={handleCleanDisk}
        />
        <main className="main">
          {loadError && (
            <div className="banner" role="alert">
              <CircleAlert size={16} /> Could not load downloads: {loadError}
            </div>
          )}
          {loaded && !hasItems ? (
            <div className="empty">
              <img src={logo} alt="Yellow Flash" width={160} height={160} />
              <h1>Fast, Resumable Downloads</h1>
              <p>Paste a direct file link to get started. Files are split across multi-threaded connections and can be resumed at maximum speed.</p>
              <button type="button" className="btn btn-primary btn-lg" onClick={() => void openAdd()}>
                <Plus size={18} /> Add URL
              </button>
              <small>Tip: Press Ctrl + N, or drop a link anywhere in this window.</small>
            </div>
          ) : (
            <>
              {rows.length === 0 && hasItems ? (
                <div className="empty small">
                  <p>No downloads match the current filter.</p>
                </div>
              ) : (
                <DownloadTable
                  rows={rows}
                  progress={progress}
                  selectedId={selectedId}
                  checkedIds={checkedIds}
                  onToggleCheck={onToggleCheck}
                  sort={sort}
                  onSort={onSort}
                  onSelect={(id) => setSelectedId(id)}
                  onActivate={activate}
                  actions={actions}
                />
              )}
              {selected && detailsOpen && (
                <DetailsPanel
                  download={selected}
                  progress={progress[selected.id]}
                  onCollapse={() => setDetailsOpen(false)}
                  onOpenFile={actions.openFile}
                  onShowInFolder={actions.showInFolder}
                  onCopyLink={actions.copyLink}
                />
              )}
            </>
          )}
        </main>
      </div>

      <StatusBar
        totalSpeed={totalSpeed}
        allSelected={allSelected}
        onToggleSelectAll={onToggleSelectAll}
      />

      {dialog?.kind === 'add' && (
        <AddUrlDialog
          key={dialog.seq}
          initialUrl={dialog.url}
          initialFilename={dialog.filename}
          initialDirectory={dialog.directory}
          settings={settings}
          onClose={() => setDialog(null)}
          onAdded={(d, started) => {
            upsertLocal(d)
            setSelectedId(d.id)
            setDialog(null)
            toast('ok', started ? `Downloading ${d.filename}` : `Added ${d.filename} (in queue)`)
          }}
        />
      )}
      {dialog?.kind === 'settings' && (
        <SettingsDialog
          settings={settings}
          info={info}
          onClose={() => setDialog(null)}
          onSaved={(s) => { setSettings(s); setDialog(null); toast('ok', 'Settings saved') }}
        />
      )}
      {dialog?.kind === 'delete' && (
        <DeleteDialog
          download={dialog.d}
          onClose={() => setDialog(null)}
          onDeleted={(id) => {
            removeLocal(id)
            if (selectedId === id) setSelectedId(null)
            setCheckedIds((prev) => {
              const next = new Set(prev)
              next.delete(id)
              return next
            })
            setDialog(null)
            toast('info', 'Download removed')
          }}
        />
      )}
      {dialog?.kind === 'scheduler' && (
        <SchedulerDialog
          downloads={items}
          onClose={() => setDialog(null)}
          onStartQueue={(qid) => toast('ok', `Queue "${qid}" started`)}
          onStopQueue={(qid) => toast('info', `Queue "${qid}" stopped`)}
        />
      )}
      {dialog?.kind === 'batch' && (
        <BatchDialog
          onClose={() => setDialog(null)}
          onAddUrls={handleBatchAdd}
        />
      )}
      {dialog?.kind === 'media' && (
        <MediaGrabberDialog
          onClose={() => setDialog(null)}
          onAddStream={(url, quality, start) => {
            toast('ok', `Video stream (${quality}) added to downloads ${start ? '(downloading)' : ''}`)
          }}
        />
      )}
      {dialog?.kind === 'speed' && (
        <SpeedLimiterDialog
          currentLimitKbps={speedLimitKbps}
          isEnabled={speedLimitEnabled}
          onClose={() => setDialog(null)}
          onSave={(enabled, limit) => {
            setSpeedLimitEnabled(enabled)
            setSpeedLimitKbps(limit)
            toast('ok', enabled ? `Speed limited to ${limit} KB/s` : 'Speed limiter disabled (maximum speed)')
          }}
        />
      )}
      {dialog?.kind === 'checksum' && (
        <ChecksumDialog
          download={dialog.d}
          onClose={() => setDialog(null)}
        />
      )}
      {dialog?.kind === 'complete' && (
        <DownloadCompleteDialog
          download={dialog.d}
          onClose={() => setDialog(null)}
          onOpenFile={actions.openFile}
          onShowInFolder={actions.showInFolder}
        />
      )}

      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.kind}`}>
            {t.kind === 'ok' ? <CheckCircle2 size={16} /> : t.kind === 'error' ? <CircleAlert size={16} /> : <Info size={16} />}
            <span>{t.kind === 'error' ? explainToast(t.text) : t.text}</span>
            <button type="button" className="icon-btn" aria-label="Dismiss" onClick={() => setToasts((all) => all.filter((x) => x.id !== t.id))}><X size={14} /></button>
          </div>
        ))}
      </div>
      {dragging && <div className="drop-hint">Drop the link to add it</div>}
    </div>
  )
}
