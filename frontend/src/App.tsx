import { useEffect, useMemo, useState, type FormEvent } from 'react'
import { Activity, ArrowDownToLine, BadgeCheck, Check, CircleAlert, Clock3, FileArchive, FileAudio2, FileImage, FileText, FileVideo2, FolderOpen, Gauge, HardDriveDownload, LayoutDashboard, Pause, Play, Plus, Search, ShieldCheck, Trash2, X } from 'lucide-react'
import type { Download, Status } from './types'

type View = 'dashboard' | 'all' | 'active' | 'queued' | 'completed' | 'failed'
const statusLabels: Record<Status, string> = { QUEUED: 'Queued', INITIALIZING: 'Starting', DOWNLOADING: 'Downloading', PAUSING: 'Pausing', PAUSED: 'Paused', VERIFYING: 'Finishing', COMPLETED: 'Completed', FAILED: 'Failed', CANCELLED: 'Cancelled' }

function formatBytes(value: number) {
  if (value < 0) return 'Unknown size'
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']; const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${(value / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}
function fileIcon(name: string) {
  const ext = name.split('.').pop()?.toLowerCase()
  if (['mp4', 'mkv', 'mov', 'webm'].includes(ext ?? '')) return FileVideo2
  if (['mp3', 'wav', 'flac', 'aac'].includes(ext ?? '')) return FileAudio2
  if (['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg'].includes(ext ?? '')) return FileImage
  if (['zip', '7z', 'rar', 'tar', 'gz'].includes(ext ?? '')) return FileArchive
  return FileText
}

export default function App() {
  const [items, setItems] = useState<Download[]>([])
  const [view, setView] = useState<View>('dashboard')
  const [search, setSearch] = useState('')
  const [url, setUrl] = useState('')
  const [filename, setFilename] = useState('')
  const [destination, setDestination] = useState('')
  const [adding, setAdding] = useState(false)
  const [error, setError] = useState('')

  const refresh = async () => { try { setItems(await window.go.main.App.ListDownloads()) } catch { /* Wails bridge becomes available after startup. */ } }
  useEffect(() => {
    void refresh()
    const timer = window.setInterval(() => void refresh(), 1800)
    const off = window.runtime?.EventsOn('downloads:updated', item => setItems(current => { const found = current.some(row => row.id === item.id); return found ? current.map(row => row.id === item.id ? item : row) : [item, ...current] }))
    return () => { window.clearInterval(timer); off?.() }
  }, [])

  const counts = useMemo(() => ({ active: items.filter(d => ['DOWNLOADING', 'INITIALIZING', 'VERIFYING'].includes(d.status)).length, queued: items.filter(d => d.status === 'QUEUED').length, completed: items.filter(d => d.status === 'COMPLETED').length, failed: items.filter(d => d.status === 'FAILED').length }), [items])
  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    return items.filter(d => {
      const matchesView = view === 'all' || view === 'dashboard' || (view === 'active' && ['DOWNLOADING', 'INITIALIZING', 'VERIFYING', 'PAUSED', 'PAUSING'].includes(d.status)) || (view === 'queued' && d.status === 'QUEUED') || (view === 'completed' && d.status === 'COMPLETED') || (view === 'failed' && ['FAILED', 'CANCELLED'].includes(d.status))
      return matchesView && (!query || `${d.filename} ${d.url}`.toLowerCase().includes(query))
    }).sort((a, b) => Date.parse(b.createdAt) - Date.parse(a.createdAt))
  }, [items, search, view])

  const submit = async (event: FormEvent) => {
    event.preventDefault(); if (!url.trim()) return
    setAdding(true); setError('')
    try { await window.go.main.App.AddDownload(url.trim(), destination.trim(), filename.trim()); setUrl(''); setFilename(''); await refresh() }
    catch (e) { setError(String(e)); }
    finally { setAdding(false) }
  }
  const chooseFolder = async () => { try { const path = await window.go.main.App.SelectDirectory(); if (path) setDestination(path) } catch (e) { setError(String(e)) } }
  const action = async (id: string, task: 'pause' | 'resume' | 'cancel') => {
    try { const api = window.go.main.App; if (task === 'pause') await api.PauseDownload(id); else if (task === 'resume') await api.ResumeDownload(id); else await api.CancelDownload(id); await refresh() }
    catch (e) { setError(String(e)) }
  }

  const nav: { id: View; label: string; icon: typeof LayoutDashboard; count?: number }[] = [
    { id: 'dashboard', label: 'Overview', icon: LayoutDashboard }, { id: 'all', label: 'All downloads', icon: HardDriveDownload, count: items.length },
    { id: 'active', label: 'In progress', icon: Activity, count: counts.active }, { id: 'queued', label: 'Queued', icon: Clock3, count: counts.queued },
    { id: 'completed', label: 'Completed', icon: BadgeCheck, count: counts.completed }, { id: 'failed', label: 'Attention', icon: CircleAlert, count: counts.failed },
  ]
  const heading = view === 'dashboard' ? 'Overview' : nav.find(item => item.id === view)?.label ?? 'Downloads'

  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><div className="brand-mark"><ArrowDownToLine size={19} strokeWidth={2.5} /></div><div><strong>Downwell</strong><span>DOWNLOAD MANAGER</span></div></div>
      <div className="nav-caption">LIBRARY</div>
      <nav>{nav.map(item => <button key={item.id} className={`nav-item ${view === item.id ? 'selected' : ''}`} onClick={() => setView(item.id)}><item.icon size={17} /><span>{item.label}</span>{item.count !== undefined && <small>{item.count}</small>}</button>)}</nav>
      <div className="sidebar-bottom"><div className="security-card"><div className="security-icon"><ShieldCheck size={16} /></div><div><strong>Private by design</strong><span>Downloads stay on this device</span></div><Check size={14} className="security-check" /></div><div className="connection"><span className="connection-dot" /> Engine ready <span className="version">v0.1</span></div></div>
    </aside>

    <main className="main-area">
      <header className="topbar"><div className="breadcrumbs"><span>Workspace</span><span className="crumb-slash">/</span><strong>{heading}</strong></div><div className="top-actions"><div className="engine-pill"><span /> Engine online</div><div className="avatar">D</div></div></header>
      <div className="content">
        <div className="page-heading"><div><div className="eyebrow">YOUR DOWNLOAD SPACE</div><h1>{heading}</h1><p>Everything you download, organized in one place.</p></div><div className="date-chip"><Activity size={15} /> Live activity</div></div>

        <form className="capture-card" onSubmit={submit}>
          <div className="capture-head"><div className="capture-icon"><Plus size={17} /></div><div><strong>Start a new download</strong><span>Paste a file link to add it to your queue</span></div><div className="secure-tag">HTTP / HTTPS</div></div>
          <div className="capture-controls"><div className="url-wrap"><ArrowDownToLine size={17} /><input value={url} onChange={e => setUrl(e.target.value)} placeholder="Paste a download link (https://...)" type="url" required /></div><button className="primary-button" disabled={adding || !url.trim()}>{adding ? <span className="spinner" /> : <><span>Download</span><ArrowDownToLine size={16} /></>}</button></div>
          <div className="capture-options"><input value={filename} onChange={e => setFilename(e.target.value)} placeholder="File name (optional)" /><div className="destination-picker"><FolderOpen size={15} /><input value={destination} onChange={e => setDestination(e.target.value)} placeholder="Choose download folder" /><button type="button" onClick={chooseFolder}>Browse</button></div></div>
          {error && <div className="error-line"><CircleAlert size={14} />{error}<button type="button" onClick={() => setError('')}><X size={14} /></button></div>}
        </form>

        {view === 'dashboard' && <section className="stats-grid">
          <div className="stat-card stat-active"><div className="stat-top"><span>IN PROGRESS</span><div><Activity size={16} /></div></div><strong>{counts.active}</strong><small>Transfers running now</small></div>
          <div className="stat-card"><div className="stat-top"><span>WAITING IN QUEUE</span><div><Clock3 size={16} /></div></div><strong>{counts.queued}</strong><small>Ready when you are</small></div>
          <div className="stat-card"><div className="stat-top"><span>COMPLETED</span><div><BadgeCheck size={16} /></div></div><strong>{counts.completed}</strong><small>Files downloaded</small></div>
          <div className="stat-card"><div className="stat-top"><span>CONNECTIONS</span><div><Gauge size={16} /></div></div><strong>{items.filter(d => d.status === 'DOWNLOADING').reduce((sum,d) => sum + d.connections,0)}</strong><small>Active file segments</small></div>
        </section>}

        <section className="downloads-section">
          <div className="section-heading"><div><div className="section-title">{view === 'dashboard' ? 'Recent downloads' : 'Downloads'} <span className="total-count">{filtered.length}</span></div><p>{view === 'dashboard' ? 'Your latest file activity at a glance.' : `Browse and manage ${heading.toLowerCase()}.`}</p></div><div className="list-tools"><div className="search-box"><Search size={15} /><input value={search} onChange={e => setSearch(e.target.value)} placeholder="Search files" /></div></div></div>
          <div className="download-table"><div className="table-header"><span>FILE</span><span>STATUS</span><span>SIZE</span><span>ADDED</span><span></span></div>
            {filtered.length === 0 ? <div className="empty-state"><div className="empty-icon"><ArrowDownToLine size={21} /></div><strong>{search ? 'No matching downloads' : 'Your list is clear'}</strong><span>{search ? 'Try another file name or link.' : 'Add a file link above and it will show up here.'}</span></div> : filtered.slice(0, view === 'dashboard' ? 6 : undefined).map(item => {
              const Icon = fileIcon(item.filename); const progress = item.size > 0 ? Math.min(100, item.downloaded / item.size * 100) : 0
              return <div className="download-row" key={item.id}><div className="file-cell"><div className={`file-icon ${item.status === 'COMPLETED' ? 'file-done' : ''}`}><Icon size={18} /></div><div className="file-meta"><strong title={item.filename}>{item.filename}</strong><span title={item.url}>{item.url.replace(/^https?:\/\//, '').split('/')[0]}</span>{['DOWNLOADING','INITIALIZING','VERIFYING'].includes(item.status) && <div className="progress-track"><div style={{width: `${progress}%`}} /></div>}</div></div>
                <div className="status-cell"><span className={`status-badge status-${item.status.toLowerCase()}`}><i />{statusLabels[item.status]}</span>{item.status === 'DOWNLOADING' && <small>{progress.toFixed(0)}%</small>}{item.status === 'FAILED' && <span className="error-tip" title={item.error}><CircleAlert size={13} /></span>}</div>
                <div className="size-cell"><strong>{formatBytes(item.downloaded)}{item.size > 0 && item.status !== 'COMPLETED' ? <span> / {formatBytes(item.size)}</span> : ''}</strong></div><div className="date-cell">{new Date(item.createdAt).toLocaleDateString(undefined,{month:'short',day:'numeric'})}</div>
                <div className="row-actions">{item.status === 'DOWNLOADING' || item.status === 'INITIALIZING' ? <button title="Pause" onClick={() => void action(item.id,'pause')}><Pause size={15}/></button> : ['PAUSED','FAILED','QUEUED'].includes(item.status) ? <button title="Resume" onClick={() => void action(item.id,'resume')}><Play size={15}/></button> : null}{!['COMPLETED','CANCELLED'].includes(item.status) && <button title="Cancel" onClick={() => void action(item.id,'cancel')}><Trash2 size={15}/></button>}</div>
              </div>
            })}
          </div>
          {view === 'dashboard' && items.length > 6 && <button className="view-all" onClick={() => setView('all')}>View all downloads <span>→</span></button>}
        </section>
        <footer className="footer"><span>DOWNWELL DESKTOP</span><span>Built for files that get there.</span><span className="footer-right"><ShieldCheck size={13}/> Local-first transfers</span></footer>
      </div>
    </main>
  </div>
}
