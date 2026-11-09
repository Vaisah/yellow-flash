import { useMemo, useState } from 'react'
import { CheckSquare, Copy, FilePlus, Layers, Plus, Square } from 'lucide-react'
import { Modal } from './Modal'

interface Props {
  onClose: () => void
  onAddUrls: (urls: string[], start: boolean) => void
}

export function BatchDialog({ onClose, onAddUrls }: Props) {
  const [mode, setMode] = useState<'pattern' | 'multiline'>('pattern')

  // Mode 1: Pattern
  const [patternUrl, setPatternUrl] = useState(
    'https://cdn.example.com/gallery/photo_*.jpg'
  )
  const [useLetters, setUseLetters] = useState(false)
  const [fromVal, setFromVal] = useState('1')
  const [toVal, setToVal] = useState('10')
  const [wildcardLen, setWildcardLen] = useState('2')

  // Mode 2: Multiline text
  const [rawText, setRawText] = useState('')

  // Computed generated URLs
  const generatedUrls = useMemo(() => {
    if (mode === 'multiline') {
      return rawText
        .split('\n')
        .map((s) => s.trim())
        .filter((s) => /^https?:\/\//i.test(s))
    }

    // Pattern mode
    if (!patternUrl.includes('*')) return []
    const results: string[] = []

    if (useLetters) {
      const startCode = (fromVal.toLowerCase() || 'a').charCodeAt(0)
      const endCode = (toVal.toLowerCase() || 'z').charCodeAt(0)
      if (startCode <= endCode && endCode - startCode < 200) {
        for (let c = startCode; c <= endCode; c++) {
          const char = String.fromCharCode(c)
          results.push(patternUrl.replace('*', char))
        }
      }
    } else {
      const startNum = parseInt(fromVal, 10)
      const endNum = parseInt(toVal, 10)
      const padLen = Math.max(1, parseInt(wildcardLen, 10) || 1)
      if (!isNaN(startNum) && !isNaN(endNum) && startNum <= endNum && endNum - startNum < 500) {
        for (let i = startNum; i <= endNum; i++) {
          const padded = String(i).padStart(padLen, '0')
          results.push(patternUrl.replace('*', padded))
        }
      }
    }
    return results
  }, [mode, patternUrl, useLetters, fromVal, toVal, wildcardLen, rawText])

  const [selectedUrls, setSelectedUrls] = useState<Set<string>>(new Set())

  // Select all by default when URLs change
  useMemo(() => {
    setSelectedUrls(new Set(generatedUrls))
  }, [generatedUrls])

  const toggleUrl = (u: string) => {
    const next = new Set(selectedUrls)
    if (next.has(u)) next.delete(u)
    else next.add(u)
    setSelectedUrls(next)
  }

  const toggleAll = () => {
    if (selectedUrls.size === generatedUrls.length) {
      setSelectedUrls(new Set())
    } else {
      setSelectedUrls(new Set(generatedUrls))
    }
  }

  const handleSubmit = (start: boolean) => {
    const toAdd = Array.from(selectedUrls)
    if (toAdd.length === 0) return
    onAddUrls(toAdd, start)
    onClose()
  }

  return (
    <Modal
      title="Add Batch Download"
      icon={<Layers size={16} />}
      onClose={onClose}
      width={680}
      footer={
        <>
          <button
            type="button"
            className="btn"
            onClick={() => handleSubmit(false)}
            disabled={selectedUrls.size === 0}
          >
            Add to Queue ({selectedUrls.size})
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => handleSubmit(true)}
            disabled={selectedUrls.size === 0}
          >
            Start All Now ({selectedUrls.size})
          </button>
        </>
      }
    >
      <div className="batch-dialog-content">
        {/* Mode selector */}
        <div className="batch-mode-toggle">
          <button
            type="button"
            className={`batch-tab ${mode === 'pattern' ? 'active' : ''}`}
            onClick={() => setMode('pattern')}
          >
            Address with Wildcard Pattern (*)
          </button>
          <button
            type="button"
            className={`batch-tab ${mode === 'multiline' ? 'active' : ''}`}
            onClick={() => setMode('multiline')}
          >
            Paste Multiple Links List
          </button>
        </div>

        {mode === 'pattern' ? (
          <div className="batch-pattern-form">
            <label className="field">
              <span>Address pattern (use * for the wildcard)</span>
              <input
                value={patternUrl}
                onChange={(e) => setPatternUrl(e.target.value)}
                placeholder="https://example.com/files/chapter_*.pdf"
                spellCheck={false}
              />
            </label>

            <div className="field-row" style={{ alignItems: 'flex-end' }}>
              <label className="field narrow">
                <span>From</span>
                <input
                  value={fromVal}
                  onChange={(e) => setFromVal(e.target.value)}
                  placeholder="1"
                />
              </label>

              <label className="field narrow">
                <span>To</span>
                <input
                  value={toVal}
                  onChange={(e) => setToVal(e.target.value)}
                  placeholder="10"
                />
              </label>

              {!useLetters && (
                <label className="field narrow">
                  <span>Wildcard size</span>
                  <input
                    value={wildcardLen}
                    onChange={(e) => setWildcardLen(e.target.value)}
                    placeholder="2"
                    title="Minimum number of digits (e.g. 2 for 01, 02...)"
                  />
                </label>
              )}

              <label className="toggle" style={{ paddingBottom: '8px' }}>
                <input
                  type="checkbox"
                  checked={useLetters}
                  onChange={(e) => setUseLetters(e.target.checked)}
                />
                <span>Use letters (a-z)</span>
              </label>
            </div>
          </div>
        ) : (
          <div className="batch-multiline-form">
            <label className="field">
              <span>Paste URLs (one address per line)</span>
              <textarea
                value={rawText}
                onChange={(e) => setRawText(e.target.value)}
                placeholder="https://site.com/file1.zip&#10;https://site.com/file2.zip&#10;https://site.com/file3.zip"
                rows={5}
                className="field-textarea"
                spellCheck={false}
              />
            </label>
          </div>
        )}

        {/* Preview and select URLs list */}
        <div className="batch-preview-section">
          <div className="batch-preview-head">
            <span>
              Preview ({selectedUrls.size} of {generatedUrls.length} selected):
            </span>
            <button
              type="button"
              className="btn-link"
              onClick={toggleAll}
              disabled={generatedUrls.length === 0}
            >
              {selectedUrls.size === generatedUrls.length ? 'Uncheck All' : 'Check All'}
            </button>
          </div>

          <div className="batch-preview-list">
            {generatedUrls.length === 0 ? (
              <div className="batch-empty">
                Enter a valid pattern with an asterisk (*) or paste URLs to generate a list.
              </div>
            ) : (
              generatedUrls.map((u) => {
                const isSelected = selectedUrls.has(u)
                return (
                  <div
                    key={u}
                    className={`batch-preview-item ${isSelected ? 'checked' : ''}`}
                    onClick={() => toggleUrl(u)}
                  >
                    <div className={`custom-checkbox-box ${isSelected ? 'checked' : ''}`}>
                      {isSelected && (
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
                    </div>
                    <span className="batch-url-text" title={u}>
                      {u}
                    </span>
                  </div>
                )
              })
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}

