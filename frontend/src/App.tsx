import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { DragEvent } from 'react'
import { CheckCircle2, CircleAlert, Info, Plus, X } from 'lucide-react'
import { backend, copyText, errorMessage } from './api'
import { useDownloads } from './hooks/useDownloads'
import { CATEGORIES, categoryOf } from './lib/categories'
import type { Category } from './lib/categories'
import { explainError } from './lib/errors'
import { Sidebar, StatusBar, Toolbar } from './components/Chrome'
import type { StatusFilter } from './components/Chrome'
import { DownloadTable, sortRows } from './components/DownloadTable'
import type { RowActions, Sort, SortKey } from './components/DownloadTable'
import { DetailsPanel } from './components/DetailsPanel'
import { AddUrlDialog, DeleteDialog, SettingsDialog } from './components/Dialogs'
import { canCancel, canDelete, canPause, canResume, isActive } from './types'
import type { AppInfo, Download, Settings } from './types'
import logo from './assets/yellow-flash-256.png'

type Dialog = { kind: 'add'; url: string } | { kind: 'settings' } | { kind: 'delete'; d: Download } | null
interface Toast { id: number; kind: 'ok' | 'error' | 'info'; text: string }

const DEFAULT_SETTINGS: Settings = { downloadDirectory: '', connections: 8, startImmediately: true, useClipboard: true }

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

/** Error toasts keep the action but use the friendly explanation when one applies. */
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
  const [detailsOpen, setDetailsOpen] = useState(true)
  const [dialog, setDialog] = useState<Dialog>(null)
  const [toasts, setToasts] = useState<Toast[]>([])
  const [dragging, setDragging] = useState(false)
  const searchRef = useRef<HTMLInputElement>(null)
  const toastId = useRef(0)

  const toast = useCallback((kind: Toast['kind'], text: string) => {
    const id = ++toastId.current
    setToasts((t) => [...t.slice(-3), { id, kind, text }])
    window.setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), kind === 'error' ? 7000 : 3500)
  }, [])

  useEffect(() => {
    backend().GetSettings().then(setSettings).catch((e) => toast('error', `Could not load settings: ${errorMessage(e)}`))
    backend().Info().then(setInfo).catch(() => undefined)
  }, [toast])

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
      c[categoryOf(d.filename)]++
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

  const selected = items.find((d) => d.id === selectedId) ?? null
  const totalSpeed = Object.values(progress).reduce((sum, p) => sum + (p.speed > 0 ? p.speed : 0), 0)

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
    copyLink: (d) => void call(async () => { await copyText(d.url); toast('info', 'Link copied') }, 'Could not copy'),
  }), [call, toast])

  const openAdd = useCallback(async (prefill?: string) => {
    let url = prefill ?? ''
    if (!url && settings.useClipboard) url = await backend().ClipboardURL().catch(() => '')
    setDialog({ kind: 'add', url })
  }, [settings.useClipboard])

  const activate = useCallback((d: Download) => {
    if (d.status === 'COMPLETED') actions.openFile(d)
    else if (canResume(d.status)) actions.resume(d)
    else setDetailsOpen(true)
  }, [actions])

  const toggle = useCallback((d: Download) => {
    if (canPause(d.status)) actions.pause(d)
    else if (canResume(d.status)) actions.resume(d)
  }, [actions])

  const onSort = (key: SortKey) =>
    setSort((s) => (s.key === key ? { key, dir: s.dir === 'asc' ? 'desc' : 'asc' } : { key, dir: key === 'name' ? 'asc' : 'desc' }))

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
        />
        <main className="main">
          {loadError && <div className="banner" role="alert"><CircleAlert size={16} /> Could not load downloads: {loadError}</div>}
          {loaded && !hasItems ? (
            <div className="empty">
              <img src={logo} alt="Yellow Flash" width={180} height={180} />
              <h1>Fast, resumable downloads</h1>
              <p>Paste a direct file link to get started. Files are split across several connections and can be paused, resumed and recovered after a crash.</p>
              <button className="btn btn-primary btn-lg" onClick={() => void openAdd()}><Plus size={18} /> Add URL</button>
              <small>Tip: press <kbd>Ctrl</kbd> + <kbd>N</kbd>, or drop a link anywhere in this window.</small>
            </div>
          ) : (
            <>
              {rows.length === 0 && hasItems ? (
                <div className="empty small"><p>No downloads match the current filter.</p></div>
              ) : (
                <DownloadTable
                  rows={rows}
                  progress={progress}
                  selectedId={selectedId}
                  sort={sort}
                  onSort={onSort}
                  onSelect={(id) => { setSelectedId(id); setDetailsOpen(true) }}
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
        active={statusCounts.active}
        waiting={statusCounts.waiting}
        completed={statusCounts.completed}
        total={items.length}
        connected={!loadError}
      />

      {dialog?.kind === 'add' && (
        <AddUrlDialog
          initialUrl={dialog.url}
          settings={settings}
          onClose={() => setDialog(null)}
          onAdded={(d, started) => {
            upsertLocal(d)
            setSelectedId(d.id)
            setDetailsOpen(true)
            setDialog(null)
            toast('ok', started ? `Downloading ${d.filename}` : `Added ${d.filename} (not started)`)
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
            setDialog(null)
            toast('info', 'Download removed')
          }}
        />
      )}

      <div className="toasts" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.kind}`}>
            {t.kind === 'ok' ? <CheckCircle2 size={16} /> : t.kind === 'error' ? <CircleAlert size={16} /> : <Info size={16} />}
            <span>{t.kind === 'error' ? explainToast(t.text) : t.text}</span>
            <button className="icon-btn" aria-label="Dismiss" onClick={() => setToasts((all) => all.filter((x) => x.id !== t.id))}><X size={14} /></button>
          </div>
        ))}
      </div>
      {dragging && <div className="drop-hint">Drop the link to add it</div>}
    </div>
  )
}
