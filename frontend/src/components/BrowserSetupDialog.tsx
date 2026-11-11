import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Check, CheckCircle2, Copy, ExternalLink, Loader2, Puzzle } from 'lucide-react'
import { Modal } from './Modal'
import { backend, copyText, errorMessage } from '../api'
import type { BrowserSetup, BrowserTarget } from '../types'

interface Props {
  onClose: () => void
}

/** Where Developer mode is on the browser's extensions page. */
const developerModeAt = (name: string) => (name === 'Microsoft Edge' ? 'in the left column' : 'at the top right')

/**
 * Connect your browsers: adds the extension to each installed browser.
 * Browsers don't let a program add an extension by itself, so each one is
 * opened at the page where the person confirms it.
 */
export function BrowserSetupDialog({ onClose }: Props) {
  const [setup, setSetup] = useState<BrowserSetup | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  // The browser whose steps are shown, and how it is being added.
  const [open, setOpen] = useState<{ name: string; mode: 'store' | 'unpacked' } | null>(null)
  const [copied, setCopied] = useState(false)

  const refresh = useCallback(() => {
    backend().GetBrowserSetup().then(setSetup).catch((e) => setError(errorMessage(e)))
  }, [])

  // The extension connects on its own once added: watch for it.
  useEffect(() => {
    refresh()
    const timer = window.setInterval(refresh, 2000)
    return () => window.clearInterval(timer)
  }, [refresh])

  const add = async (b: BrowserTarget) => {
    setBusy(b.name)
    setError('')
    try {
      const mode = await backend().AddExtensionTo(b.name)
      setOpen({ name: b.name, mode })
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy('')
    }
  }

  const copyFolder = async () => {
    if (!setup?.extensionDir) return
    await copyText(setup.extensionDir)
    setCopied(true)
    window.setTimeout(() => setCopied(false), 2000)
  }

  const finish = () => {
    void backend().FinishBrowserSetup().catch(() => undefined)
    onClose()
  }

  const browsers = setup?.browsers ?? []
  const anyConnected = browsers.some((b) => b.connected)

  return (
    <Modal
      title="Connect Your Browsers"
      icon={<Puzzle size={16} />}
      onClose={finish}
      width={600}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <span className="foot-hint">You can come back here from Options → General.</span>
          <button type="button" className="btn btn-primary" onClick={finish}>
            {anyConnected ? 'Done' : 'Not now'}
          </button>
        </>
      }
    >
      <div className="browser-setup">
        <p className="browser-setup-intro">
          Yellow Flash catches your browser's downloads through its extension. Add it to the browsers you use, and
          their downloads come here automatically, split across connections and resumable.
        </p>

        {setup?.portable && (
          <div className="link-problem">
            <AlertTriangle size={18} />
            <div>
              <strong>This is a portable copy</strong>
              <p>A portable copy changes nothing outside its folder, so it can't connect to browsers. Install Yellow Flash to use the extension.</p>
            </div>
          </div>
        )}
        {setup && !setup.portable && !setup.hostReady && (
          <div className="link-problem">
            <AlertTriangle size={18} />
            <div>
              <strong>The browser helper is missing</strong>
              <p>yellowflash-nmhost should be beside Yellow Flash. Reinstall Yellow Flash to restore it.</p>
            </div>
          </div>
        )}

        {setup && browsers.length === 0 && (
          <p className="browser-setup-empty">No browser that can run the extension was found on this computer.</p>
        )}
        {!setup && !error && (
          <p className="browser-setup-empty"><Loader2 size={14} className="spin" /> Looking for your browsers…</p>
        )}

        <ul className="browser-list">
          {browsers.map((b) => {
            const available = !!b.store || b.unpacked
            const showing = open?.name === b.name && !b.connected
            return (
              <li key={b.name} className={`browser-row ${b.connected ? 'connected' : ''}`}>
                <div className="browser-row-main">
                  <span className="browser-row-name">{b.name}</span>
                  {b.connected ? (
                    <span className="browser-row-status ok">
                      <CheckCircle2 size={15} /> Connected{b.version ? ` (extension ${b.version})` : ''}
                    </span>
                  ) : available ? (
                    <button
                      type="button"
                      className="btn btn-sm btn-primary"
                      onClick={() => void add(b)}
                      disabled={!!busy || !setup?.hostReady}
                    >
                      {busy === b.name ? <Loader2 size={13} className="spin" /> : <ExternalLink size={13} />}
                      {' '}{b.store ? `Add to ${b.name}` : 'Set up'}
                    </button>
                  ) : (
                    <span className="browser-row-status muted">
                      {b.family === 'firefox' ? 'Coming soon: the add-on is on its way to addons.mozilla.org' : 'Not available yet'}
                    </span>
                  )}
                </div>

                {showing && open.mode === 'store' && (
                  <div className="browser-steps">
                    <p>{b.name} opened the extension's page. Click <strong>Add</strong> there and confirm. This row turns green when it connects.</p>
                  </div>
                )}
                {showing && open.mode === 'unpacked' && (
                  <ol className="browser-steps">
                    <li>{b.name} opened its <strong>Extensions</strong> page.</li>
                    <li>Turn on <strong>Developer mode</strong> ({developerModeAt(b.name)}).</li>
                    <li>
                      Click <strong>Load unpacked</strong>, paste this folder into the folder box (it's already copied)
                      and choose <strong>Select Folder</strong>:
                      <div className="input-with-button browser-steps-folder">
                        <input readOnly value={setup?.extensionDir ?? ''} className="field-mono" onFocus={(e) => e.target.select()} />
                        <button type="button" className="btn btn-sm" onClick={() => void copyFolder()}>
                          {copied ? <Check size={13} /> : <Copy size={13} />} {copied ? 'Copied' : 'Copy'}
                        </button>
                      </div>
                    </li>
                    <li>This row turns green when the extension connects.</li>
                  </ol>
                )}
              </li>
            )
          })}
        </ul>
      </div>
    </Modal>
  )
}
