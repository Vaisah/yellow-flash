import { useState } from 'react'
import { Gauge, Sliders } from 'lucide-react'
import { Modal } from './Modal'

interface Props {
  onClose: () => void
  currentLimitKbps: number
  isEnabled: boolean
  onSave: (enabled: boolean, limitKbps: number) => void
}

export function SpeedLimiterDialog({ onClose, currentLimitKbps, isEnabled, onSave }: Props) {
  const [enabled, setEnabled] = useState(isEnabled)
  const [speed, setSpeed] = useState(currentLimitKbps || 1024)

  const PRESETS = [256, 512, 1024, 2048, 5120, 10240]

  const handleSave = () => {
    onSave(enabled, speed)
    onClose()
  }

  return (
    <Modal
      title="Speed Limiter Settings"
      icon={<Gauge size={16} />}
      onClose={onClose}
      width={480}
      footer={
        <>
          <button type="button" className="btn" onClick={onClose}>
            Cancel
          </button>
          <button type="button" className="btn btn-primary" onClick={handleSave}>
            Apply Settings
          </button>
        </>
      }
    >
      <div className="speed-limiter-content">
        <label className="toggle">
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
          />
          <strong>Turn on Yellow Flash Speed Limiter</strong>
        </label>

        <p className="field-note">
          Limits the maximum download bandwidth across all active threads to leave room for web browsing or gaming.
        </p>

        <div className={`limiter-box ${!enabled ? 'disabled' : ''}`}>
          <label className="field">
            <span>Maximum download speed (KB/s):</span>
            <div className="input-with-button">
              <input
                type="number"
                min={32}
                max={100000}
                step={64}
                disabled={!enabled}
                value={speed}
                onChange={(e) => setSpeed(Math.max(1, Number(e.target.value)))}
                className="field-number-lg"
              />
              <span className="unit-label">KB/sec ({(speed / 1024).toFixed(1)} MB/s)</span>
            </div>
          </label>

          <input
            type="range"
            min={128}
            max={20480}
            step={128}
            disabled={!enabled}
            value={speed}
            onChange={(e) => setSpeed(Number(e.target.value))}
            className="field-range"
          />

          <div className="speed-presets">
            <span className="presets-label">Quick Presets:</span>
            <div className="presets-buttons">
              {PRESETS.map((p) => (
                <button
                  key={p}
                  type="button"
                  disabled={!enabled}
                  className={`btn-preset ${speed === p ? 'active' : ''}`}
                  onClick={() => setSpeed(p)}
                >
                  {p >= 1024 ? `${p / 1024} MB/s` : `${p} KB/s`}
                </button>
              ))}
            </div>
          </div>
        </div>
      </div>
    </Modal>
  )
}

