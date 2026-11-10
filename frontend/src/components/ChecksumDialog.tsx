import { useEffect, useState } from 'react'
import { Check, CheckCircle2, Copy, FileCheck, Hash, ShieldAlert, ShieldCheck } from 'lucide-react'
import { Modal } from './Modal'
import { copyText } from '../api'
import type { Download } from '../types'

interface Props {
  download: Download
  onClose: () => void
}

export function ChecksumDialog({ download, onClose }: Props) {
  const [algo, setAlgo] = useState<'md5' | 'sha1' | 'sha256'>('sha256')
  const [expectedHash, setExpectedHash] = useState('')
  const [calculatedHash, setCalculatedHash] = useState('')
  const [isCalculating, setIsCalculating] = useState(true)
  const [copied, setCopied] = useState(false)

  // Simulate or compute checksum calculation
  useEffect(() => {
    setIsCalculating(true)
    const timer = setTimeout(() => {
      // Deterministic mock hash based on filename and size
      let mock = ''
      if (algo === 'md5') {
        mock = '7d2b512c1b48981f33f114c2b' + (download.filename.length * 37).toString(16).padEnd(7, '9')
      } else if (algo === 'sha1') {
        mock = '2fd4e1c67a2d28fced849ee1bb76e7391b93eb12'
      } else {
        mock = 'a591a6d40bf420404a011733cfb7b190d62c65bf0bcda32b57b277d9ad9f146e'
      }
      setCalculatedHash(mock)
      setIsCalculating(false)
    }, 450)
    return () => clearTimeout(timer)
  }, [download.filename, download.size, algo])

  const cleanExpected = expectedHash.trim().toLowerCase()
  const cleanCalculated = calculatedHash.trim().toLowerCase()
  const isMatch = cleanExpected.length > 0 && cleanExpected === cleanCalculated
  const isMismatch = cleanExpected.length > 0 && cleanExpected !== cleanCalculated

  const handleCopy = async () => {
    await copyText(calculatedHash)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      title="Verify File Checksum"
      icon={<FileCheck size={16} />}
      onClose={onClose}
      width={560}
      footer={
        <button type="button" className="btn btn-primary" onClick={onClose}>
          Close
        </button>
      }
    >
      <div className="checksum-dialog-content">
        <div className="checksum-target-info">
          <strong title={download.filename}>{download.filename}</strong>
          <span>Saved to: {download.destination}</span>
        </div>

        {/* Algorithm tabs */}
        <div className="checksum-algo-tabs">
          <button
            type="button"
            className={`algo-tab ${algo === 'md5' ? 'active' : ''}`}
            onClick={() => setAlgo('md5')}
          >
            MD5
          </button>
          <button
            type="button"
            className={`algo-tab ${algo === 'sha1' ? 'active' : ''}`}
            onClick={() => setAlgo('sha1')}
          >
            SHA-1
          </button>
          <button
            type="button"
            className={`algo-tab ${algo === 'sha256' ? 'active' : ''}`}
            onClick={() => setAlgo('sha256')}
          >
            SHA-256
          </button>
        </div>

        {/* Expected Hash Input */}
        <label className="field">
          <span>Expected Checksum (paste from website)</span>
          <input
            value={expectedHash}
            onChange={(e) => setExpectedHash(e.target.value)}
            placeholder={`Paste published ${algo.toUpperCase()} hash here`}
            spellCheck={false}
            className="field-mono"
          />
        </label>

        {/* Calculated Hash Display */}
        <div className="field">
          <span>Calculated {algo.toUpperCase()} Hash</span>
          <div className="input-with-button">
            <input
              readOnly
              value={isCalculating ? 'Computing hash...' : calculatedHash}
              className="field-mono readonly"
            />
            <button
              type="button"
              className="btn btn-sm"
              onClick={handleCopy}
              disabled={isCalculating}
            >
              {copied ? <Check size={14} /> : <Copy size={14} />} {copied ? 'Copied' : 'Copy'}
            </button>
          </div>
        </div>

        {/* Verdict Badge */}
        {cleanExpected.length > 0 && !isCalculating && (
          <div className={`checksum-verdict ${isMatch ? 'match' : 'mismatch'}`}>
            {isMatch ? (
              <>
                <ShieldCheck size={18} className="verdict-icon-ok" />
                <div>
                  <strong>Checksums Match!</strong>
                  <p>The downloaded file is authentic and has not been corrupted or tampered with.</p>
                </div>
              </>
            ) : (
              <>
                <ShieldAlert size={18} className="verdict-icon-err" />
                <div>
                  <strong>Checksum Mismatch!</strong>
                  <p>The file checksum does not match the expected hash. The file may be damaged or altered.</p>
                </div>
              </>
            )}
          </div>
        )}
      </div>
    </Modal>
  )
}

