import { useEffect, useState } from 'react'
import { AlertTriangle, Download, Film, Globe, Loader2, Music, Video } from 'lucide-react'
import { Modal } from './Modal'
import { backend, errorMessage } from '../api'
import { formatBytes } from '../lib/format'
import type { VideoLookUp } from '../types'

interface Props {
  onClose: () => void
  /** A video download started (it runs with yt-dlp, outside the list). */
  onStarted: (title: string, quality: string) => void
}

function duration(s: number): string {
  if (!s || s <= 0) return ''
  const h = Math.floor(s / 3600)
  const m = Math.floor((s % 3600) / 60)
  const sec = Math.floor(s % 60)
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}` : `${m}:${String(sec).padStart(2, '0')}`
}

const isAudio = (label: string, ext: string) => /audio|mp3|m4a|opus/i.test(label) || ['mp3', 'm4a', 'opus', 'ogg', 'wav'].includes(ext)

export function MediaGrabberDialog({ onClose, onStarted }: Props) {
  const [pageUrl, setPageUrl] = useState('')
  const [isInspecting, setIsInspecting] = useState(false)
  const [found, setFound] = useState<VideoLookUp | null>(null)
  const [selected, setSelected] = useState(0)
  const [error, setError] = useState('')
  const [hasYtDlp, setHasYtDlp] = useState<boolean | null>(null)
  const [installing, setInstalling] = useState(false)
  const [starting, setStarting] = useState(false)

  useEffect(() => {
    backend().GetHelpers()
      .then((hs) => setHasYtDlp(hs.some((h) => h.name === 'yt-dlp' && h.exe !== '')))
      .catch(() => setHasYtDlp(false))
  }, [])

  const choices = found?.choices ?? []

  const install = async () => {
    setInstalling(true)
    setError('')
    try {
      const h = await backend().InstallHelper('yt-dlp')
      setHasYtDlp(h.exe !== '')
    } catch (e) {
      setError(`Could not install yt-dlp: ${errorMessage(e)}`)
    } finally {
      setInstalling(false)
    }
  }

  const handleInspect = async () => {
    if (!pageUrl.trim()) return
    setIsInspecting(true)
    setError('')
    setFound(null)
    try {
      const v = await backend().LookUpVideo(pageUrl.trim(), navigator.language || 'en')
      setFound(v)
      setSelected(0)
      if (v.list) setError(`This page lists ${v.list.total > 0 ? v.list.total : 'several'} videos; open one video's own page to download it.`)
      else if (!v.choices?.length) setError('No downloadable format was found on this page.')
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setIsInspecting(false)
    }
  }

  const handleDownload = async () => {
    const choice = choices[selected]
    if (!found || !choice) return
    setStarting(true)
    setError('')
    try {
      await backend().StartVideoDownload({
        url: pageUrl.trim(), title: found.title, choice, directory: '',
        subs: { langs: [], automatic: false, embed: false },
      })
      onStarted(found.title, choice.label)
      onClose()
    } catch (e) {
      setError(errorMessage(e))
      setStarting(false)
    }
  }

  const ready = !!found && choices.length > 0 && !found.list

  return (
    <Modal
      title="Download Video & Media Stream"
      icon={<Film size={16} />}
      onClose={onClose}
      width={640}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" onClick={() => void handleDownload()} disabled={!ready || starting}>
            {starting ? <Loader2 size={14} className="spin" /> : <Download size={14} />} Start Download
          </button>
        </>
      }
    >
      <div className="media-grabber-content">
        {hasYtDlp === false && (
          <div className="link-problem">
            <AlertTriangle size={18} />
            <div>
              <strong>Videos are downloaded with yt-dlp</strong>
              <p>It isn't installed yet. Yellow Flash can install its own copy (checked against its publisher's signature).</p>
              <button type="button" className="btn btn-sm" onClick={() => void install()} disabled={installing}>
                {installing ? <Loader2 size={14} className="spin" /> : <Download size={14} />} Install yt-dlp
              </button>
            </div>
          </div>
        )}

        <label className="field">
          <span>Video or Web Page Address</span>
          <div className="input-with-button">
            <input
              data-autofocus
              value={pageUrl}
              onChange={(e) => setPageUrl(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') void handleInspect() }}
              placeholder="https://youtube.com/watch?v=... or https://vimeo.com/..."
              spellCheck={false}
            />
            <button
              type="button"
              className="btn btn-primary"
              onClick={() => void handleInspect()}
              disabled={isInspecting || !hasYtDlp || !pageUrl.trim()}
            >
              {isInspecting ? <Loader2 size={14} className="spin" /> : <Globe size={14} />} Inspect Streams
            </button>
          </div>
        </label>

        {found && !found.list && (
          <div className="media-streams-section">
            <div className="media-preview-card">
              <div className="media-thumb-placeholder">
                <Film size={28} className="media-thumb-icon" />
              </div>
              <div className="media-meta">
                <strong>{found.title}</strong>
                <span>
                  {duration(found.duration) && `Duration: ${duration(found.duration)} min`}
                  {found.needsFfmpeg && ' · Install ffmpeg for the best qualities (video and audio come apart)'}
                </span>
              </div>
            </div>

            {choices.length > 0 && <span className="streams-header">Available Formats & Qualities:</span>}

            <div className="streams-list">
              {choices.map((s, i) => {
                const isSelected = selected === i
                return (
                  <div key={s.selector + s.label} className={`stream-item ${isSelected ? 'selected' : ''}`} onClick={() => setSelected(i)}>
                    <div className="stream-radio">
                      <div className={`radio-circle ${isSelected ? 'checked' : ''}`} />
                    </div>
                    <div className="stream-icon-col">
                      {isAudio(s.label, s.ext) ? <Music size={16} /> : <Video size={16} />}
                    </div>
                    <div className="stream-info-col">
                      <span className="stream-title">{s.label}</span>
                      <span className="stream-sub">{s.ext.toUpperCase()}</span>
                    </div>
                    <span className="stream-size">{s.size > 0 ? `~${formatBytes(s.size)}` : ''}</span>
                  </div>
                )
              })}
            </div>
          </div>
        )}
      </div>
    </Modal>
  )
}
