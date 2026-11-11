import { useEffect, useRef, useState } from 'react'
import {
  AlertTriangle,
  CheckCircle2,
  ExternalLink,
  FolderOpen,
  Globe,
  HardDrive,
  KeyRound,
  Layers,
  Link2,
  Loader2,
  Network,
  Plus,
  Settings as SettingsIcon,
  ShieldCheck,
  Sliders,
  Trash2,
  Wifi,
  Zap,
} from 'lucide-react'
import { Modal } from './Modal'
import { backend, errorMessage } from '../api'
import { explainError } from '../lib/errors'
import { formatBytes } from '../lib/format'
import { iconFor } from '../lib/categories'
import type { AppInfo, Download, LinkInfo, Options, Settings, SiteLogin } from '../types'
import logo from '../assets/yellow-flash-256.png'

const CONNECTION_CHOICES = [1, 2, 4, 6, 8, 12, 16, 24, 32]

function looksLikeUrl(text: string): boolean {
  try {
    const u = new URL(text.trim())
    return (u.protocol === 'http:' || u.protocol === 'https:' || u.protocol === 'ftp:') && u.hostname.length > 0
  } catch {
    return false
  }
}

/* ------------------------------------------------------------------ */

interface AddProps {
  initialUrl: string
  initialFilename?: string
  initialDirectory?: string
  settings: Settings
  onClose: () => void
  onAdded: (d: Download, started: boolean) => void
}

export function AddUrlDialog({
  initialUrl,
  initialFilename,
  initialDirectory,
  settings,
  onClose,
  onAdded,
}: AddProps) {
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
  const [needAuth, setNeedAuth] = useState(false)
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
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
        url: url.trim(),
        directory: directory.trim(),
        filename: filenameEdited ? filename.trim() : '',
        connections,
        start,
        ...(needAuth && username.trim() ? { username: username.trim(), password } : {}),
      })
      onAdded(d, start)
    } catch (e) {
      setSubmitError(errorMessage(e))
      setSubmitting(false)
    }
  }

  const FileIcon = iconFor(filename || info?.filename || '')
  const unwrapped = info !== null && info.url !== url.trim()
  const problem = webPage
    ? explainError('web page, not a downloadable file')
    : inspectError
    ? explainError(inspectError)
    : null

  return (
    <Modal
      title="Download File Info"
      icon={<Link2 size={16} />}
      onClose={onClose}
      width={640}
      footer={
        <>
          {submitError && <span className="foot-error" role="alert">{submitError}</span>}
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => submit(false)}
            disabled={!valid || submitting || webPage}
          >
            Download Later
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => submit(true)}
            disabled={!valid || submitting || webPage}
          >
            {submitting ? <Loader2 size={15} className="spin" /> : <Zap size={15} />} Start Download
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
        {url !== '' && !valid && <p className="field-note warn">Enter a full http:// or https:// address.</p>}

        <div className={`link-card ${problem ? 'is-problem' : ''}`} aria-live="polite">
          {!valid && <p className="muted">Paste a link to detect the file name, size and server capabilities.</p>}
          {valid && inspecting && <p className="muted"><Loader2 size={14} className="spin" /> Checking link info…</p>}
          {valid && !inspecting && info && !webPage && (
            <div className="link-info">
              <span className="link-icon"><FileIcon size={20} /></span>
              <div>
                <strong>{info.filename}</strong>
                <div className="chips">
                  <span className="chip">{info.size >= 0 ? formatBytes(info.size) : 'Unknown size'}</span>
                  {info.mimeType && <span className="chip">{info.mimeType.split(';')[0]}</span>}
                  <span className={`chip ${info.rangeSupported ? 'chip-ok' : 'chip-warn'}`}>
                    {info.rangeSupported ? <><CheckCircle2 size={12} /> Multi-connection Resumable</> : 'Single Connection'}
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
            <span>File Name</span>
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
          <span>Save To</span>
          <div className="input-with-button">
            <input value={directory} onChange={(e) => setDirectory(e.target.value)} spellCheck={false} />
            <button type="button" className="btn" onClick={browse}><FolderOpen size={15} /> Browse</button>
          </div>
        </label>

        {/* Server authentication toggle */}
        <label className="toggle">
          <input
            type="checkbox"
            checked={needAuth}
            onChange={(e) => setNeedAuth(e.target.checked)}
          />
          <span>Use server authorization (User & Password)</span>
        </label>

        {needAuth && (
          <div className="field-row">
            <label className="field grow">
              <span>Username</span>
              <input
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="Username"
                spellCheck={false}
              />
            </label>
            <label className="field grow">
              <span>Password</span>
              <input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Password"
              />
            </label>
          </div>
        )}

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
  const [activeTab, setActiveTab] = useState<'general' | 'filetypes' | 'saveto' | 'connection' | 'logins' | 'about'>('general')
  const [draft, setDraft] = useState<Settings>(settings)
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  // Options → General and File Types, as the backend holds them.
  const [options, setOptions] = useState<Options | null>(null)
  const setOption = <K extends keyof Options>(key: K, value: Options[K]) =>
    setOptions((o) => (o ? { ...o, [key]: value } : o))
  const [siteLogins, setSiteLogins] = useState<SiteLogin[]>([])
  useEffect(() => {
    backend().GetOptions().then(setOptions).catch((e) => setError(errorMessage(e)))
    backend().ListSiteLogins().then(setSiteLogins).catch((e) => setError(errorMessage(e)))
  }, [])
  const [newSite, setNewSite] = useState('')
  const [newUser, setNewUser] = useState('')
  const [newPass, setNewPass] = useState('')

  const set = <K extends keyof Settings>(key: K, value: Settings[K]) =>
    setDraft((d) => ({ ...d, [key]: value }))

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
      const saved = await backend().SaveSettings(draft)
      if (options) await backend().SaveOptions(options)
      onSaved(saved)
    } catch (e) {
      setError(errorMessage(e))
      setSaving(false)
    }
  }

  // Saved at once: the password goes to the system's keychain.
  const addLogin = async () => {
    if (!newSite.trim() || !newUser.trim()) return
    setError('')
    try {
      setSiteLogins(await backend().SaveSiteLogin(newSite.trim(), newUser.trim(), newPass))
      setNewSite('')
      setNewUser('')
      setNewPass('')
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  const removeLogin = async (site: string) => {
    setError('')
    try {
      setSiteLogins(await backend().RemoveSiteLogin(site))
    } catch (e) {
      setError(errorMessage(e))
    }
  }

  return (
    <Modal
      title="Configuration Options"
      icon={<SettingsIcon size={16} />}
      onClose={onClose}
      width={680}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" onClick={save} disabled={saving}>
            OK
          </button>
        </>
      }
    >
      <div className="options-modal-tabs">
        <button
          type="button"
          className={`opt-tab ${activeTab === 'general' ? 'active' : ''}`}
          onClick={() => setActiveTab('general')}
        >
          General
        </button>
        <button
          type="button"
          className={`opt-tab ${activeTab === 'filetypes' ? 'active' : ''}`}
          onClick={() => setActiveTab('filetypes')}
        >
          File Types
        </button>
        <button
          type="button"
          className={`opt-tab ${activeTab === 'saveto' ? 'active' : ''}`}
          onClick={() => setActiveTab('saveto')}
        >
          Save To
        </button>
        <button
          type="button"
          className={`opt-tab ${activeTab === 'connection' ? 'active' : ''}`}
          onClick={() => setActiveTab('connection')}
        >
          Connection
        </button>
        <button
          type="button"
          className={`opt-tab ${activeTab === 'logins' ? 'active' : ''}`}
          onClick={() => setActiveTab('logins')}
        >
          Site Logins
        </button>
        <button
          type="button"
          className={`opt-tab ${activeTab === 'about' ? 'active' : ''}`}
          onClick={() => setActiveTab('about')}
        >
          About
        </button>
      </div>

      <div className="options-modal-body">
        {activeTab === 'general' && (
          <div className="options-pane">
            <label className="toggle">
              <input
                type="checkbox"
                checked={options?.launchOnStartup ?? false}
                disabled={!options}
                onChange={(e) => setOption('launchOnStartup', e.target.checked)}
              />
              <span>Launch Yellow Flash on system startup</span>
            </label>

            <label className="toggle">
              <input
                type="checkbox"
                checked={options?.browserCapture ?? false}
                disabled={!options}
                onChange={(e) => setOption('browserCapture', e.target.checked)}
              />
              <span>Capture downloads from web browsers (Chrome, Edge, Firefox, Brave, Opera)</span>
            </label>

            <label className="toggle">
              <input
                type="checkbox"
                checked={draft.useClipboard}
                onChange={(e) => set('useClipboard', e.target.checked)}
              />
              <span>Automatically detect and capture links copied to the clipboard</span>
            </label>

            <label className="toggle">
              <input
                type="checkbox"
                checked={draft.startImmediately}
                onChange={(e) => set('startImmediately', e.target.checked)}
              />
              <span>Start downloads immediately when added</span>
            </label>

            <label className="toggle">
              <input
                type="checkbox"
                checked={options?.showComplete ?? true}
                disabled={!options}
                onChange={(e) => setOption('showComplete', e.target.checked)}
              />
              <span>Show Download Complete notification window when finished</span>
            </label>
          </div>
        )}

        {activeTab === 'filetypes' && (
          <div className="options-pane">
            <label className="field">
              <span>Automatically capture downloads for following file extensions:</span>
              <textarea
                value={options?.fileTypes ?? ''}
                disabled={!options}
                onChange={(e) => setOption('fileTypes', e.target.value)}
                rows={5}
                className="field-textarea field-mono"
                spellCheck={false}
              />
            </label>
            <p className="field-note">Separate multiple file extensions with a space.</p>
          </div>
        )}

        {activeTab === 'saveto' && (
          <div className="options-pane">
            <label className="field">
              <span>Default Download Folder</span>
              <div className="input-with-button">
                <input
                  value={draft.downloadDirectory}
                  onChange={(e) => set('downloadDirectory', e.target.value)}
                  spellCheck={false}
                />
                <button type="button" className="btn" onClick={browse}>
                  <FolderOpen size={15} /> Browse
                </button>
              </div>
            </label>

            <div className="category-folders-info">
              <span>New downloads are saved here unless you choose another folder when adding them.
                The sidebar's categories (Music, Videos, Compressed, Programs, Documents) sort the list by file type.</span>
            </div>
          </div>
        )}

        {activeTab === 'connection' && (
          <div className="options-pane">
            <label className="field">
              <span>Maximum Connections per Download: <b>{draft.connections}</b></span>
              <select
                value={draft.connections}
                onChange={(e) => set('connections', Number(e.target.value))}
              >
                {CONNECTION_CHOICES.map((n) => (
                  <option key={n} value={n}>
                    {n} Connections (Multi-Threaded Turbo)
                  </option>
                ))}
              </select>
              <small className="field-note">
                Higher values maximize speed across high-bandwidth fiber/cable connections.
              </small>
            </label>
          </div>
        )}

        {activeTab === 'logins' && (
          <div className="options-pane">
            <span className="scheduler-box-label">Saved Site Credentials:</span>
            <div className="logins-table-wrap">
              <table className="queue-table">
                <thead>
                  <tr>
                    <th>Site / Host</th>
                    <th>User</th>
                    <th>Action</th>
                  </tr>
                </thead>
                <tbody>
                  {siteLogins.length === 0 && (
                    <tr><td colSpan={3} className="queue-empty-td">No saved logins. Downloads from a site listed here use its login.</td></tr>
                  )}
                  {siteLogins.map((item) => (
                    <tr key={item.site}>
                      <td>{item.site}</td>
                      <td>{item.user}</td>
                      <td>
                        <button
                          type="button"
                          className="icon-btn-sm queue-remove-btn"
                          onClick={() => void removeLogin(item.site)}
                          title="Remove Login"
                        >
                          <Trash2 size={13} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="add-login-row">
              <input
                placeholder="Site (e.g. host.com)"
                value={newSite}
                onChange={(e) => setNewSite(e.target.value)}
                className="field-sm"
              />
              <input
                placeholder="User"
                value={newUser}
                onChange={(e) => setNewUser(e.target.value)}
                className="field-sm"
              />
              <input
                type="password"
                placeholder="Password"
                value={newPass}
                onChange={(e) => setNewPass(e.target.value)}
                className="field-sm"
              />
              <button type="button" className="btn btn-sm" onClick={() => void addLogin()}>
                Add
              </button>
            </div>
          </div>
        )}

        {activeTab === 'about' && (
          <div className="options-pane">
            <div className="about">
              <img src={logo} alt="Yellow Flash" width={64} height={64} />
              <div>
                <strong>Yellow Flash Download Manager</strong>
                <span>Version {info?.version ?? '—'}</span>
                <span>High-Speed Resumable Multi-Segment Engine</span>
                {info?.dataDir && <span className="mono">Storage: {info.dataDir}</span>}
              </div>
            </div>
          </div>
        )}
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
      title="Remove Download"
      icon={<Trash2 size={16} />}
      onClose={onClose}
      width={460}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button type="button" className="btn" onClick={onClose} data-autofocus>
            Cancel
          </button>
          <button type="button" className="btn btn-danger" onClick={confirm}>
            Remove
          </button>
        </>
      }
    >
      <p className="confirm-text">
        Remove <strong>{download.filename}</strong> from the download list?
      </p>
      {completed ? (
        <label className="toggle">
          <input
            type="checkbox"
            checked={deleteFile}
            onChange={(e) => setDeleteFile(e.target.checked)}
          />
          <span>Also delete the downloaded file from disk</span>
        </label>
      ) : (
        <p className="muted">Any partially downloaded temporary data will be deleted.</p>
      )}
    </Modal>
  )
}

/* ------------------------------------------------------------------ */

interface CompleteProps {
  download: Download
  onClose: () => void
  onOpenFile: (d: Download) => void
  onShowInFolder: (d: Download) => void
}

export function DownloadCompleteDialog({
  download,
  onClose,
  onOpenFile,
  onShowInFolder,
}: CompleteProps) {
  const Icon = iconFor(download.filename)

  return (
    <Modal
      title="Download Complete"
      icon={<CheckCircle2 size={16} />}
      onClose={onClose}
      width={500}
      footer={
        <>
          <button
            type="button"
            className="btn"
            onClick={() => {
              onShowInFolder(download)
              onClose()
            }}
          >
            <FolderOpen size={14} /> Open Folder
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => {
              onOpenFile(download)
              onClose()
            }}
          >
            <ExternalLink size={14} /> Open File
          </button>
          <button type="button" className="btn" onClick={onClose}>
            Close
          </button>
        </>
      }
    >
      <div className="complete-modal-content">
        <div className="complete-icon-badge">
          <Icon size={32} />
        </div>
        <div className="complete-info">
          <strong>{download.filename}</strong>
          <span>Size: {formatBytes(download.size)}</span>
          <span className="complete-path">Saved to: {download.destination}</span>
        </div>
      </div>
    </Modal>
  )
}
