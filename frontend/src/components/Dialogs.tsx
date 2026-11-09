import { useEffect, useRef, useState } from 'react'
import { AlertTriangle, CheckCircle2, FolderOpen, Link2, Loader2, Settings as SettingsIcon, Trash2, Zap } from 'lucide-react'
import { Modal } from './Modal'
import { backend, errorMessage } from '../api'
import { explainError } from '../lib/errors'
import { formatBytes } from '../lib/format'
import { iconFor } from '../lib/categories'
import type { AppInfo, Download, LinkInfo, Settings } from '../types'
import logo from '../assets/yellow-flash-256.png'

const CONNECTION_CHOICES = [1, 2, 4, 6, 8, 12, 16]

function looksLikeUrl(text: string): boolean {
  try {
    const u = new URL(text.trim())
    return (u.protocol === 'http:' || u.protocol === 'https:') && u.hostname.length > 0
  } catch {
    return false
  }
}

/* ------------------------------------------------------------------ */

interface AddProps {
  initialUrl: string
  /** From a command line's /f: kept over the name the link suggests. */
  initialFilename?: string
  /** From a command line's /p. */
  initialDirectory?: string
  settings: Settings
  onClose: () => void
  onAdded: (d: Download, started: boolean) => void
}

export function AddUrlDialog({ initialUrl, initialFilename, initialDirectory, settings, onClose, onAdded }: AddProps) {
  const [url, setUrl] = useState(initialUrl)
  const [filename, setFilename] = useState(initialFilename ?? '')
  const [filenameEdited, setFilenameEdited] = useState(!!initialFilename)
  const [directory, setDirectory] = useState(initialDirectory ?? settings.downloadDirectory)
  const [connections, setConnections] = useState(settings.connections)
  const [info, setInfo] = useState<LinkInfo | null>(null)
  const [inspecting, setInspecting] = useState(false)
  const [inspectError, setInspectError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [submitError, setSubmitError] = useState('')
  const requestId = useRef(0)
  const editedRef = useRef(false)
  editedRef.current = filenameEdited

  const valid = looksLikeUrl(url)

  useEffect(() => {
    setInfo(null)
    setInspectError('')
    setInspecting(false)
    if (!valid) return
    const id = ++requestId.current
    const timer = window.setTimeout(async () => {
      setInspecting(true)
      try {
        const result = await backend().InspectLink(url.trim())
        if (id !== requestId.current) return
        setInfo(result)
        if (!editedRef.current) setFilename(result.filename)
      } catch (e) {
        if (id === requestId.current) setInspectError(errorMessage(e))
      } finally {
        if (id === requestId.current) setInspecting(false)
      }
    }, 450)
    return () => window.clearTimeout(timer)
  }, [url, valid])

  const browse = async () => {
    try {
      const dir = await backend().SelectDirectory()
      if (dir) setDirectory(dir)
    } catch (e) {
      setSubmitError(errorMessage(e))
    }
  }

  const webPage = info?.webPage ?? false

  const submit = async (start: boolean) => {
    if (!valid || submitting || webPage) return
    setSubmitting(true)
    setSubmitError('')
    try {
      const d = await backend().AddDownload({
        url: url.trim(), directory: directory.trim(), filename: filenameEdited ? filename.trim() : '', connections, start,
      })
      onAdded(d, start)
    } catch (e) {
      setSubmitError(errorMessage(e))
      setSubmitting(false)
    }
  }

  const FileIcon = iconFor(filename || info?.filename || '')
  const unwrapped = info !== null && info.url !== url.trim()
  const problem = webPage ? explainError('web page, not a downloadable file') : inspectError ? explainError(inspectError) : null

  return (
    <Modal
      title="Add download"
      icon={<Link2 size={16} />}
      onClose={onClose}
      width={600}
      footer={
        <>
          {submitError && <span className="foot-error" role="alert">{submitError}</span>}
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn" onClick={() => submit(false)} disabled={!valid || submitting || webPage}>Download later</button>
          <button className="btn btn-primary" onClick={() => submit(true)} disabled={!valid || submitting || webPage}>
            {submitting ? <Loader2 size={15} className="spin" /> : <Zap size={15} />} Start download
          </button>
        </>
      }
    >
      <form onSubmit={(e) => { e.preventDefault(); void submit(settings.startImmediately) }}>
        <label className="field">
          <span>Address</span>
          <input
            data-autofocus
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://example.com/file.zip"
            spellCheck={false}
            aria-invalid={url !== '' && !valid}
          />
        </label>
        {url !== '' && !valid && <p className="field-note warn">Enter a full http:// or https:// link.</p>}

        <div className={`link-card ${problem ? 'is-problem' : ''}`} aria-live="polite">
          {!valid && <p className="muted">Paste a link to see the file name, size and type before downloading.</p>}
          {valid && inspecting && <p className="muted"><Loader2 size={14} className="spin" /> Checking link…</p>}
          {valid && !inspecting && info && !webPage && (
            <div className="link-info">
              <span className="link-icon"><FileIcon size={20} /></span>
              <div>
                <strong>{info.filename}</strong>
                <div className="chips">
                  <span className="chip">{info.size >= 0 ? formatBytes(info.size) : 'Unknown size'}</span>
                  {info.mimeType && <span className="chip">{info.mimeType.split(';')[0]}</span>}
                  <span className={`chip ${info.rangeSupported ? 'chip-ok' : 'chip-warn'}`}>
                    {info.rangeSupported ? <><CheckCircle2 size={12} /> Resumable, multi-connection</> : 'Single connection, no resume'}
                  </span>
                </div>
                {unwrapped && <p className="field-note">Found the direct file link inside the address you pasted.</p>}
              </div>
            </div>
          )}
          {valid && !inspecting && problem && (
            <div className="link-problem">
              <AlertTriangle size={18} />
              <div>
                <strong>{problem.title}</strong>
                <p>{problem.hint}</p>
                {!webPage && <p className="muted">You can still try to add it.</p>}
              </div>
            </div>
          )}
        </div>

        <div className="field-row">
          <label className="field grow">
            <span>File name</span>
            <input
              value={filename}
              onChange={(e) => { setFilename(e.target.value); setFilenameEdited(true) }}
              placeholder="Detected automatically"
              spellCheck={false}
            />
          </label>
          <label className="field narrow">
            <span>Connections</span>
            <select value={connections} onChange={(e) => setConnections(Number(e.target.value))}>
              {CONNECTION_CHOICES.map((n) => <option key={n} value={n}>{n}</option>)}
            </select>
          </label>
        </div>
        <label className="field">
          <span>Save to</span>
          <div className="input-with-button">
            <input value={directory} onChange={(e) => setDirectory(e.target.value)} spellCheck={false} />
            <button type="button" className="btn" onClick={browse}><FolderOpen size={15} /> Browse</button>
          </div>
        </label>
        <button type="submit" hidden aria-hidden="true" tabIndex={-1} />
      </form>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */

interface SettingsProps {
  settings: Settings
  info: AppInfo | null
  onClose: () => void
  onSaved: (s: Settings) => void
}

export function SettingsDialog({ settings, info, onClose, onSaved }: SettingsProps) {
  const [draft, setDraft] = useState<Settings>(settings)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const set = <K extends keyof Settings>(key: K, value: Settings[K]) => setDraft((d) => ({ ...d, [key]: value }))

  const browse = async () => {
    try {
      const dir = await backend().SelectDirectory()
      if (dir) set('downloadDirectory', dir)
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  const save = async () => {
    setSaving(true)
    setError('')
    try {
      onSaved(await backend().SaveSettings(draft))
    } catch (e) {
      setError(errorMessage(e))
      setSaving(false)
    }
  }

  return (
    <Modal
      title="Settings"
      icon={<SettingsIcon size={16} />}
      onClose={onClose}
      width={560}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button className="btn" onClick={onClose}>Cancel</button>
          <button className="btn btn-primary" onClick={save} disabled={saving}>Save</button>
        </>
      }
    >
      <label className="field">
        <span>Default download folder</span>
        <div className="input-with-button">
          <input value={draft.downloadDirectory} onChange={(e) => set('downloadDirectory', e.target.value)} spellCheck={false} />
          <button type="button" className="btn" onClick={browse}><FolderOpen size={15} /> Browse</button>
        </div>
      </label>
      <label className="field">
        <span>Connections per download: <b>{draft.connections}</b></span>
        <input type="range" min={1} max={16} value={draft.connections} onChange={(e) => set('connections', Number(e.target.value))} />
        <small className="field-note">More connections are faster on most servers. Lower this if a site limits you.</small>
      </label>
      <label className="toggle">
        <input type="checkbox" checked={draft.startImmediately} onChange={(e) => set('startImmediately', e.target.checked)} />
        <span>Start downloads as soon as they are added</span>
      </label>
      <label className="toggle">
        <input type="checkbox" checked={draft.useClipboard} onChange={(e) => set('useClipboard', e.target.checked)} />
        <span>Fill in links copied to the clipboard when adding a download</span>
      </label>
      <div className="about">
        <img src={logo} alt="" width={56} height={56} />
        <div>
          <strong>Yellow Flash Download Manager</strong>
          <span>Version {info?.version ?? '—'}</span>
          {info?.dataDir && <span className="mono">Data: {info.dataDir}</span>}
        </div>
      </div>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */

interface DeleteProps {
  download: Download
  onClose: () => void
  onDeleted: (id: string) => void
}

export function DeleteDialog({ download, onClose, onDeleted }: DeleteProps) {
  const [deleteFile, setDeleteFile] = useState(false)
  const [error, setError] = useState('')
  const completed = download.status === 'COMPLETED'

  const confirm = async () => {
    try {
      await backend().DeleteDownload(download.id, completed && deleteFile)
      onDeleted(download.id)
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  return (
    <Modal
      title="Remove download"
      icon={<Trash2 size={16} />}
      onClose={onClose}
      width={460}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button className="btn" onClick={onClose} data-autofocus>Cancel</button>
          <button className="btn btn-danger" onClick={confirm}>Remove</button>
        </>
      }
    >
      <p className="confirm-text">Remove <strong>{download.filename}</strong> from the list?</p>
      {completed ? (
        <label className="toggle">
          <input type="checkbox" checked={deleteFile} onChange={(e) => setDeleteFile(e.target.checked)} />
          <span>Also delete the downloaded file from disk</span>
        </label>
      ) : (
        <p className="muted">Any partially downloaded data will be deleted.</p>
      )}
    </Modal>
  )
}
