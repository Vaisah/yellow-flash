import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Calendar,
  Clock,
  Download as DownloadIcon,
  Layers,
  Loader2,
  MoveDown,
  MoveUp,
  Play,
  Plus,
  Power,
  Square,
  Trash2,
} from 'lucide-react'
import { Modal } from './Modal'
import { backend, errorMessage } from '../api'
import type { Download, NetworkSettings, Queue, Schedule } from '../types'

/** What the Schedule tab edits for one queue, until Apply. */
interface Draft {
  filesAtOnce: number
  startOnStartup: boolean
  startAtEnabled: boolean
  startTime: string
  once: boolean
  startDate: string
  days: boolean[]
  stopAtEnabled: boolean
  stopTime: string
  turnOffWhenDone: boolean
  exitAppWhenDone: boolean
}

interface Limits {
  enabled: boolean
  mb: number
  hours: number
}

interface Props {
  onClose: () => void
  downloads: Download[]
  onStartQueue?: (name: string) => void
  onStopQueue?: (name: string) => void
}

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']
const pad = (n: number) => String(n).padStart(2, '0')
const today = () => new Date().toISOString().split('T')[0]

function draftOf(q: Queue): Draft {
  const s = q.schedule
  return {
    filesAtOnce: q.filesAtOnce || 1,
    startOnStartup: !!s.startOnStartup,
    startAtEnabled: !!s.startAt,
    startTime: s.startAt ? `${pad(s.startAt.hour)}:${pad(s.startAt.minute)}` : '02:00',
    once: !!s.once,
    startDate: s.once ? `${s.once.year}-${pad(s.once.month)}-${pad(s.once.day)}` : today(),
    days: s.days?.length === 7 ? [...s.days] : [true, true, true, true, true, true, true],
    stopAtEnabled: !!s.stopAt,
    stopTime: s.stopAt ? `${pad(s.stopAt.hour)}:${pad(s.stopAt.minute)}` : '07:00',
    turnOffWhenDone: s.turnOff === 'shutdown',
    exitAppWhenDone: !!s.exitWhenDone,
  }
}

function time(t: string) {
  const [h, m] = t.split(':').map(Number)
  return { hour: h || 0, minute: m || 0, second: 0 }
}

/** The schedule a draft says, keeping what the dialog doesn't show. */
function scheduleOf(d: Draft, old: Schedule): Schedule {
  const [y, mo, day] = d.startDate.split('-').map(Number)
  return {
    ...old,
    startOnStartup: d.startOnStartup,
    startAt: d.startAtEnabled ? time(d.startTime) : null,
    days: d.days,
    once: d.startAtEnabled && d.once && y ? { year: y, month: mo, day } : null,
    stopAt: d.stopAtEnabled ? time(d.stopTime) : null,
    turnOff: d.turnOffWhenDone ? 'shutdown' : '',
    exitWhenDone: d.exitAppWhenDone,
  }
}

export function SchedulerDialog({ onClose, downloads, onStartQueue, onStopQueue }: Props) {
  const [queues, setQueues] = useState<Queue[]>([])
  const [drafts, setDrafts] = useState<Record<string, Draft>>({})
  const [limits, setLimits] = useState<Limits>({ enabled: false, mb: 10000, hours: 24 })
  const [network, setNetwork] = useState<NetworkSettings | null>(null)
  const [selectedQueueId, setSelectedQueueId] = useState<string>('main')
  const [activeTab, setActiveTab] = useState<'schedule' | 'files' | 'limits'>('schedule')
  const [adding, setAdding] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  /** The queues as the backend has them; drafts kept for those being edited. */
  const reload = useCallback(async () => {
    try {
      const qs = await backend().ListQueues()
      setQueues(qs)
      setDrafts((prev) => {
        const next: Record<string, Draft> = {}
        for (const q of qs) next[q.id] = prev[q.id] ?? draftOf(q)
        return next
      })
    } catch (e) {
      setError(errorMessage(e))
    }
  }, [])

  useEffect(() => {
    backend().GetNetworkSettings().then((n) => {
      setNetwork(n)
      setLimits({ enabled: n.dataCapEnabled, mb: n.dataCapMB || 10000, hours: n.dataCapHours || 24 })
    }).catch((e) => setError(errorMessage(e)))
  }, [])

  // The list's downloads change while the dialog is open (a queue runs).
  useEffect(() => { void reload() }, [downloads, reload])

  const activeQueue = queues.find((q) => q.id === selectedQueueId) ?? queues[0]
  const draft = activeQueue ? drafts[activeQueue.id] : undefined
  const byId = useMemo(() => new Map(downloads.map((d) => [d.id, d])), [downloads])
  const items = activeQueue ? (activeQueue.items ?? []).map((id) => byId.get(id)).filter((d): d is Download => !!d) : []
  const queued = useMemo(() => new Set(queues.flatMap((q) => q.items ?? [])), [queues])
  const addable = downloads.filter((d) => !queued.has(d.id) && d.status !== 'COMPLETED')

  const edit = (change: (d: Draft) => Draft) => {
    if (!activeQueue || !draft) return
    setDrafts((prev) => ({ ...prev, [activeQueue.id]: change({ ...draft }) }))
  }

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true)
    setError('')
    try {
      await fn()
      await reload()
    } catch (e) {
      setError(errorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  const saveQueue = async (q: Queue) => {
    const d = drafts[q.id]
    if (!d) return
    await backend().SetQueueSchedule(q.id, scheduleOf(d, q.schedule))
    if (d.filesAtOnce !== q.filesAtOnce) await backend().SetQueueFilesAtOnce(q.id, d.filesAtOnce)
  }

  const addQueue = () => {
    const name = prompt('Enter new queue name:', `Queue ${queues.length + 1}`)
    if (!name?.trim()) return
    void run(async () => {
      const q = await backend().CreateQueue(name.trim())
      setSelectedQueueId(q.id)
    })
  }

  const removeQueue = (q: Queue) => {
    if (q.builtin || !confirm(`Delete the queue "${q.name}"? Its downloads stay in the list.`)) return
    void run(async () => {
      await backend().DeleteQueue(q.id)
      setSelectedQueueId('main')
    })
  }

  /** Saves every queue's edited schedule, and the limits. */
  const apply = async () => {
    setBusy(true)
    setError('')
    try {
      for (const q of queues) await saveQueue(q)
      if (network && (network.dataCapEnabled !== limits.enabled || network.dataCapMB !== limits.mb || network.dataCapHours !== limits.hours)) {
        await backend().SaveNetworkSettings({ ...network, dataCapEnabled: limits.enabled, dataCapMB: limits.mb, dataCapHours: limits.hours })
      }
      onClose()
    } catch (e) {
      setError(errorMessage(e))
      setBusy(false)
    }
  }

  const startStop = () => {
    if (!activeQueue) return
    const q = activeQueue
    void run(async () => {
      if (q.running) {
        await backend().StopQueue(q.id)
        onStopQueue?.(q.name)
      } else {
        await saveQueue(q) // its schedule as edited, then it starts
        await backend().StartQueue(q.id)
        onStartQueue?.(q.name)
      }
    })
  }

  return (
    <Modal
      title="Scheduler & Queue Manager"
      icon={<Calendar size={16} />}
      onClose={onClose}
      width={780}
      footer={
        <>
          {error && <span className="foot-error" role="alert">{error}</span>}
          <button
            type="button"
            className={`btn ${activeQueue?.running ? 'btn-danger' : 'btn-primary'}`}
            onClick={startStop}
            disabled={!activeQueue || busy}
          >
            {activeQueue?.running ? <><Square size={14} /> Stop Queue</> : <><Play size={14} /> Start Queue Now</>}
          </button>
          <button type="button" className="btn" onClick={() => void apply()} disabled={busy}>
            {busy && <Loader2 size={14} className="spin" />} Apply & Close
          </button>
        </>
      }
    >
      <div className="scheduler-layout">
        <div className="scheduler-sidebar">
          <div className="scheduler-sidebar-head">
            <span className="scheduler-sidebar-title">Queues</span>
            <button type="button" className="icon-btn" onClick={addQueue} title="Add New Queue" disabled={busy}>
              <Plus size={15} />
            </button>
          </div>
          <div className="scheduler-queues-list">
            {queues.map((q) => (
              <div
                key={q.id}
                className={`scheduler-queue-item ${q.id === activeQueue?.id ? 'active' : ''}`}
                onClick={() => setSelectedQueueId(q.id)}
              >
                <Layers size={14} className="queue-item-icon" />
                <span className="queue-item-name">{q.name}{q.running ? ' (running)' : ''}</span>
                {!q.builtin && (
                  <button
                    type="button"
                    className="queue-del-btn"
                    onClick={(e) => {
                      e.stopPropagation()
                      removeQueue(q)
                    }}
                    title="Delete Queue"
                  >
                    <Trash2 size={13} />
                  </button>
                )}
              </div>
            ))}
          </div>
        </div>

        <div className="scheduler-content">
          <div className="scheduler-tabs">
            <button type="button" className={`scheduler-tab ${activeTab === 'schedule' ? 'active' : ''}`} onClick={() => setActiveTab('schedule')}>
              <Clock size={14} /> Schedule
            </button>
            <button type="button" className={`scheduler-tab ${activeTab === 'files' ? 'active' : ''}`} onClick={() => setActiveTab('files')}>
              <DownloadIcon size={14} /> Files in Queue ({items.length})
            </button>
            <button type="button" className={`scheduler-tab ${activeTab === 'limits' ? 'active' : ''}`} onClick={() => setActiveTab('limits')}>
              <Power size={14} /> Download Limits
            </button>
          </div>

          <div className="scheduler-body">
            {!activeQueue || !draft ? (
              <div className="scheduler-pane"><p className="muted"><Loader2 size={14} className="spin" /> Loading queues…</p></div>
            ) : (
              <>
                {activeTab === 'schedule' && (
                  <div className="scheduler-pane">
                    <label className="toggle">
                      <input type="checkbox" checked={draft.startOnStartup} onChange={(e) => edit((d) => ({ ...d, startOnStartup: e.target.checked }))} />
                      <span>Start downloading on Yellow Flash startup</span>
                    </label>

                    <div className="scheduler-box">
                      <label className="toggle">
                        <input type="checkbox" checked={draft.startAtEnabled} onChange={(e) => edit((d) => ({ ...d, startAtEnabled: e.target.checked }))} />
                        <strong>Start download at:</strong>
                      </label>
                      <div className="scheduler-time-row">
                        <input
                          type="time"
                          value={draft.startTime}
                          disabled={!draft.startAtEnabled}
                          onChange={(e) => edit((d) => ({ ...d, startTime: e.target.value }))}
                          className="field-time"
                        />
                        <label className="day-check">
                          <input type="radio" checked={!draft.once} disabled={!draft.startAtEnabled} onChange={() => edit((d) => ({ ...d, once: false }))} />
                          <span>Daily</span>
                        </label>
                        <label className="day-check">
                          <input type="radio" checked={draft.once} disabled={!draft.startAtEnabled} onChange={() => edit((d) => ({ ...d, once: true }))} />
                          <span>Once at</span>
                        </label>
                        <input
                          type="date"
                          value={draft.startDate}
                          disabled={!draft.startAtEnabled || !draft.once}
                          onChange={(e) => edit((d) => ({ ...d, startDate: e.target.value }))}
                          className="field-date"
                        />
                      </div>

                      <div className="scheduler-days-row">
                        {DAYS.map((day, idx) => (
                          <label key={day} className="day-check">
                            <input
                              type="checkbox"
                              checked={draft.days[idx]}
                              disabled={!draft.startAtEnabled || draft.once}
                              onChange={(e) => {
                                const days = [...draft.days]
                                days[idx] = e.target.checked
                                edit((d) => ({ ...d, days }))
                              }}
                            />
                            <span>{day}</span>
                          </label>
                        ))}
                      </div>
                    </div>

                    <div className="scheduler-box">
                      <label className="toggle">
                        <input type="checkbox" checked={draft.stopAtEnabled} onChange={(e) => edit((d) => ({ ...d, stopAtEnabled: e.target.checked }))} />
                        <strong>Stop download at:</strong>
                      </label>
                      <div className="scheduler-time-row">
                        <input
                          type="time"
                          value={draft.stopTime}
                          disabled={!draft.stopAtEnabled}
                          onChange={(e) => edit((d) => ({ ...d, stopTime: e.target.value }))}
                          className="field-time"
                        />
                      </div>
                    </div>

                    <div className="scheduler-box">
                      <span className="scheduler-box-label">When queue finishes:</span>
                      <div className="scheduler-options-list">
                        <label className="toggle">
                          <input type="checkbox" checked={draft.turnOffWhenDone} onChange={(e) => edit((d) => ({ ...d, turnOffWhenDone: e.target.checked }))} />
                          <span>Turn off computer when downloads complete</span>
                        </label>
                        <label className="toggle">
                          <input type="checkbox" checked={draft.exitAppWhenDone} onChange={(e) => edit((d) => ({ ...d, exitAppWhenDone: e.target.checked }))} />
                          <span>Exit Yellow Flash when complete</span>
                        </label>
                      </div>
                    </div>
                  </div>
                )}

                {activeTab === 'files' && (
                  <div className="scheduler-pane">
                    <div className="scheduler-field-inline">
                      <span>Download</span>
                      <input
                        type="number"
                        min={1}
                        max={10}
                        value={draft.filesAtOnce}
                        onChange={(e) => edit((d) => ({ ...d, filesAtOnce: Math.min(10, Math.max(1, Number(e.target.value))) }))}
                        className="field-number"
                      />
                      <span>files at the same time in this queue</span>
                    </div>

                    <div className="scheduler-field-inline">
                      <select value={adding} onChange={(e) => setAdding(e.target.value)} disabled={addable.length === 0 || busy}>
                        <option value="">{addable.length === 0 ? 'No download to add' : 'Add a download to this queue…'}</option>
                        {addable.map((d) => <option key={d.id} value={d.id}>{d.filename}</option>)}
                      </select>
                      <button
                        type="button"
                        className="btn btn-sm"
                        disabled={!adding || busy}
                        onClick={() => void run(async () => {
                          await backend().AddToQueue(activeQueue.id, [adding])
                          setAdding('')
                        })}
                      >
                        <Plus size={13} /> Add
                      </button>
                    </div>

                    <div className="queue-items-table-wrap">
                      <table className="queue-table">
                        <thead>
                          <tr>
                            <th>Order</th>
                            <th>File Name</th>
                            <th>Size</th>
                            <th>Actions</th>
                          </tr>
                        </thead>
                        <tbody>
                          {items.length === 0 ? (
                            <tr>
                              <td colSpan={4} className="queue-empty-td">
                                No files in this queue. Add one above, or from a download's right-click menu.
                              </td>
                            </tr>
                          ) : (
                            items.map((item, index) => (
                              <tr key={item.id}>
                                <td className="queue-order-num">{index + 1}</td>
                                <td className="queue-file-title" title={item.filename}>{item.filename}</td>
                                <td className="queue-file-size">{item.size > 0 ? `${(item.size / 1024 / 1024).toFixed(1)} MB` : '—'}</td>
                                <td>
                                  <div className="queue-item-actions">
                                    <button type="button" className="icon-btn-sm" disabled={index === 0 || busy}
                                      onClick={() => void run(() => backend().MoveInQueue(item.id, -1))} title="Move Up">
                                      <MoveUp size={13} />
                                    </button>
                                    <button type="button" className="icon-btn-sm" disabled={index === items.length - 1 || busy}
                                      onClick={() => void run(() => backend().MoveInQueue(item.id, 1))} title="Move Down">
                                      <MoveDown size={13} />
                                    </button>
                                    <button type="button" className="icon-btn-sm queue-remove-btn" disabled={busy}
                                      onClick={() => void run(() => backend().RemoveFromQueue(item.id))} title="Remove from queue">
                                      <Trash2 size={13} />
                                    </button>
                                  </div>
                                </td>
                              </tr>
                            ))
                          )}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}

                {activeTab === 'limits' && (
                  <div className="scheduler-pane">
                    <label className="toggle">
                      <input type="checkbox" checked={limits.enabled} onChange={(e) => setLimits((l) => ({ ...l, enabled: e.target.checked }))} />
                      <strong>Enforce download quota limits</strong>
                    </label>

                    <p className="field-note">
                      Limits how much Yellow Flash downloads in a period (every queue and download together), to stay within a data cap.
                      When it is reached, downloads wait until the period ends.
                    </p>

                    <div className="scheduler-box">
                      <div className="scheduler-field-inline">
                        <span>Limit to maximum</span>
                        <input
                          type="number"
                          disabled={!limits.enabled}
                          value={limits.mb}
                          onChange={(e) => setLimits((l) => ({ ...l, mb: Math.max(1, Number(e.target.value)) }))}
                          className="field-number-lg"
                        />
                        <span>Megabytes (MB)</span>
                      </div>

                      <div className="scheduler-field-inline" style={{ marginTop: '10px' }}>
                        <span>Reset counter every</span>
                        <input
                          type="number"
                          disabled={!limits.enabled}
                          value={limits.hours}
                          onChange={(e) => setLimits((l) => ({ ...l, hours: Math.max(1, Number(e.target.value)) }))}
                          className="field-number"
                        />
                        <span>hours</span>
                      </div>
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}
