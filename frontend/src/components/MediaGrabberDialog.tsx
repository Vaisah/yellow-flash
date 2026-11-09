import { useState } from 'react'
import { Check, CheckSquare, Download, Film, Globe, Loader2, Music, Video } from 'lucide-react'
import { Modal } from './Modal'

interface StreamQuality {
  id: string
  label: string
  resolution: string
  format: string
  sizeMb: number
  hasAudio: boolean
}

interface Props {
  onClose: () => void
  onAddStream: (url: string, quality: string, start: boolean) => void
}

export function MediaGrabberDialog({ onClose, onAddStream }: Props) {
  const [pageUrl, setPageUrl] = useState('https://www.youtube.com/watch?v=dQw4w9WgXcQ')
  const [isInspecting, setIsInspecting] = useState(false)
  const [hasInspected, setHasInspected] = useState(true)
  const [selectedQuality, setSelectedQuality] = useState('1080p')

  const STREAMS: StreamQuality[] = [
    { id: '4k', label: 'Ultra HD 4K (2160p)', resolution: '3840x2160', format: 'MP4 / H.264', sizeMb: 245.5, hasAudio: true },
    { id: '1080p', label: 'Full HD (1080p)', resolution: '1920x1080', format: 'MP4 / H.264', sizeMb: 85.2, hasAudio: true },
    { id: '720p', label: 'High Definition (720p)', resolution: '1280x720', format: 'MP4 / H.264', sizeMb: 42.1, hasAudio: true },
    { id: '480p', label: 'Standard Definition (480p)', resolution: '854x480', format: 'MP4 / H.264', sizeMb: 21.0, hasAudio: true },
    { id: 'audio', label: 'Audio Only (MP3 / 320kbps)', resolution: 'Audio only', format: 'MP3 High Quality', sizeMb: 7.8, hasAudio: true },
  ]

  const handleInspect = () => {
    if (!pageUrl.trim()) return
    setIsInspecting(true)
    setTimeout(() => {
      setIsInspecting(false)
      setHasInspected(true)
    }, 600)
  }

  const handleDownload = (start: boolean) => {
    onAddStream(pageUrl, selectedQuality, start)
    onClose()
  }

  return (
    <Modal
      title="Download Video & Media Stream"
      icon={<Film size={16} />}
      onClose={onClose}
      width={640}
      footer={
        <>
          <button
            type="button"
            className="btn"
            onClick={() => handleDownload(false)}
            disabled={!hasInspected}
          >
            Download Later
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => handleDownload(true)}
            disabled={!hasInspected}
          >
            Start Download
          </button>
        </>
      }
    >
      <div className="media-grabber-content">
        <label className="field">
          <span>Video or Web Page Address</span>
          <div className="input-with-button">
            <input
              value={pageUrl}
              onChange={(e) => setPageUrl(e.target.value)}
              placeholder="https://youtube.com/watch?v=... or https://vimeo.com/..."
              spellCheck={false}
            />
            <button
              type="button"
              className="btn btn-primary"
              onClick={handleInspect}
              disabled={isInspecting}
            >
              {isInspecting ? <Loader2 size={14} className="spin" /> : <Globe size={14} />} Inspect Streams
            </button>
          </div>
        </label>

        {hasInspected && (
          <div className="media-streams-section">
            <div className="media-preview-card">
              <div className="media-thumb-placeholder">
                <Film size={28} className="media-thumb-icon" />
              </div>
              <div className="media-meta">
                <strong>Rick Astley - Never Gonna Give You Up (Official Music Video)</strong>
                <span>Source: YouTube · Duration: 3:33 min · Channel: RickAstleyVEVO</span>
              </div>
            </div>

            <span className="streams-header">Available Formats & Qualities:</span>

            <div className="streams-list">
              {STREAMS.map((s) => {
                const isSelected = selectedQuality === s.id
                return (
                  <div
                    key={s.id}
                    className={`stream-item ${isSelected ? 'selected' : ''}`}
                    onClick={() => setSelectedQuality(s.id)}
                  >
                    <div className="stream-radio">
                      <div className={`radio-circle ${isSelected ? 'checked' : ''}`} />
                    </div>
                    <div className="stream-icon-col">
                      {s.id === 'audio' ? <Music size={16} /> : <Video size={16} />}
                    </div>
                    <div className="stream-info-col">
                      <span className="stream-title">{s.label}</span>
                      <span className="stream-sub">{s.resolution} · {s.format}</span>
                    </div>
                    <span className="stream-size">~{s.sizeMb.toFixed(1)} MB</span>
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

