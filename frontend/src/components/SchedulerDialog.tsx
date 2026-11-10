import { useState } from 'react'
import {
  Calendar,
  Clock,
  Download as DownloadIcon,
  FolderOpen,
  Layers,
  MoveDown,
  MoveUp,
  Play,
  Plus,
  Power,
  Square,
  Trash2,
} from 'lucide-react'
import { Modal } from './Modal'
import type { Download } from '../types'

interface QueueItem {
  id: string
  name: string
  items: Download[]
  enabled: boolean
  maxSimultaneous: number
  startOnStartup: boolean
  startAtEnabled: boolean
  startTime: string
  startDate: string
  stopAtEnabled: boolean
  stopTime: string
  days: boolean[] // Sun, Mon, Tue, Wed, Thu, Fri, Sat
  turnOffWhenDone: boolean
  exitAppWhenDone: boolean
  limitsEnabled: boolean
  limitMB: number
  limitHours: number
}

interface Props {
  onClose: () => void
  downloads: Download[]
  onStartQueue?: (queueId: string) => void
  onStopQueue?: (queueId: string) => void
}

const DEFAULT_DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export function SchedulerDialog({ onClose, downloads, onStartQueue, onStopQueue }: Props) {
  const [queues, setQueues] = useState<QueueItem[]>([
    {
      id: 'main',
      name: 'Main Download Queue',
      items: downloads.slice(0, 4),
      enabled: true,
      maxSimultaneous: 2,
      startOnStartup: false,
      startAtEnabled: true,
      startTime: '02:00',
      startDate: new Date().toISOString().split('T')[0],
      stopAtEnabled: true,
      stopTime: '07:00',
      days: [true, true, true, true, true, true, true],
      turnOffWhenDone: false,
      exitAppWhenDone: false,
      limitsEnabled: false,
      limitMB: 10000,
      limitHours: 24,
    },
    {
      id: 'sync',
      name: 'Synchronization Queue',
      items: downloads.slice(4),
      enabled: false,
      maxSimultaneous: 1,
      startOnStartup: false,
      startAtEnabled: false,
      startTime: '00:00',
      startDate: new Date().toISOString().split('T')[0],
      stopAtEnabled: false,
      stopTime: '06:00',
      days: [false, true, true, true, true, true, false],
      turnOffWhenDone: false,
      exitAppWhenDone: false,
      limitsEnabled: false,
      limitMB: 5000,
      limitHours: 12,
    },
  ])

  const [selectedQueueId, setSelectedQueueId] = useState<string>('main')
  const [activeTab, setActiveTab] = useState<'schedule' | 'files' | 'limits'>('schedule')
  const [isRunning, setIsRunning] = useState<boolean>(false)

  const activeQueue = queues.find((q) => q.id === selectedQueueId) || queues[0]

  const updateActiveQueue = (updater: (q: QueueItem) => QueueItem) => {
    setQueues((prev) => prev.map((q) => (q.id === activeQueue.id ? updater({ ...q }) : q)))
  }

  const addQueue = () => {
    const name = prompt('Enter new queue name:', `Queue ${queues.length + 1}`)
    if (!name) return
    const newQ: QueueItem = {
      id: `queue-${Date.now()}`,
      name,
      items: [],
      enabled: true,
      maxSimultaneous: 1,
      startOnStartup: false,
      startAtEnabled: false,
      startTime: '01:00',
      startDate: new Date().toISOString().split('T')[0],
      stopAtEnabled: false,
      stopTime: '06:00',
      days: [true, true, true, true, true, true, true],
      turnOffWhenDone: false,
      exitAppWhenDone: false,
      limitsEnabled: false,
      limitMB: 5000,
      limitHours: 24,
    }
    setQueues([...queues, newQ])
    setSelectedQueueId(newQ.id)
  }

  const removeQueue = (id: string) => {
    if (queues.length <= 1) return
    if (confirm('Delete this download queue?')) {
      const remaining = queues.filter((q) => q.id !== id)
      setQueues(remaining)
      setSelectedQueueId(remaining[0].id)
    }
  }

  const moveItem = (index: number, direction: 'up' | 'down') => {
    const items = [...activeQueue.items]
    const target = direction === 'up' ? index - 1 : index + 1
    if (target < 0 || target >= items.length) return
    const temp = items[index]
    items[index] = items[target]
    items[target] = temp
    updateActiveQueue((q) => ({ ...q, items }))
  }

  const removeItem = (id: string) => {
    updateActiveQueue((q) => ({ ...q, items: q.items.filter((i) => i.id !== id) }))
  }

  return (
    <Modal
      title="Scheduler & Queue Manager"
      icon={<Calendar size={16} />}
      onClose={onClose}
      width={780}
      footer={
        <>
          <button
            type="button"
            className={`btn ${isRunning ? 'btn-danger' : 'btn-primary'}`}
            onClick={() => {
              setIsRunning(!isRunning)
              if (!isRunning) onStartQueue?.(activeQueue.id)
              else onStopQueue?.(activeQueue.id)
            }}
          >
            {isRunning ? (
              <>
                <Square size={14} /> Stop Queue
              </>
            ) : (
              <>
                <Play size={14} /> Start Queue Now
              </>
            )}
          </button>
          <button type="button" className="btn" onClick={onClose}>
            Apply & Close
          </button>
        </>
      }
    >
      <div className="scheduler-layout">
        {/* Left Queues List */}
        <div className="scheduler-sidebar">
          <div className="scheduler-sidebar-head">
            <span className="scheduler-sidebar-title">Queues</span>
            <button
              type="button"
              className="icon-btn"
              onClick={addQueue}
              title="Add New Queue"
            >
              <Plus size={15} />
            </button>
          </div>
          <div className="scheduler-queues-list">
            {queues.map((q) => (
              <div
                key={q.id}
                className={`scheduler-queue-item ${q.id === activeQueue.id ? 'active' : ''}`}
                onClick={() => setSelectedQueueId(q.id)}
              >
                <Layers size={14} className="queue-item-icon" />
                <span className="queue-item-name">{q.name}</span>
                {queues.length > 1 && q.id !== 'main' && (
                  <button
                    type="button"
                    className="queue-del-btn"
                    onClick={(e) => {
                      e.stopPropagation()
                      removeQueue(q.id)
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

        {/* Right Tabbed Configuration */}
        <div className="scheduler-content">
          <div className="scheduler-tabs">
            <button
              type="button"
              className={`scheduler-tab ${activeTab === 'schedule' ? 'active' : ''}`}
              onClick={() => setActiveTab('schedule')}
            >
              <Clock size={14} /> Schedule
            </button>
            <button
              type="button"
              className={`scheduler-tab ${activeTab === 'files' ? 'active' : ''}`}
              onClick={() => setActiveTab('files')}
            >
              <DownloadIcon size={14} /> Files in Queue ({activeQueue.items.length})
            </button>
            <button
              type="button"
              className={`scheduler-tab ${activeTab === 'limits' ? 'active' : ''}`}
              onClick={() => setActiveTab('limits')}
            >
              <Power size={14} /> Download Limits
            </button>
          </div>

          <div className="scheduler-body">
            {activeTab === 'schedule' && (
              <div className="scheduler-pane">
                <label className="toggle">
                  <input
                    type="checkbox"
                    checked={activeQueue.startOnStartup}
                    onChange={(e) =>
                      updateActiveQueue((q) => ({ ...q, startOnStartup: e.target.checked }))
                    }
                  />
                  <span>Start downloading on Yellow Flash startup</span>
                </label>

                <div className="scheduler-box">
                  <label className="toggle">
                    <input
                      type="checkbox"
                      checked={activeQueue.startAtEnabled}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({ ...q, startAtEnabled: e.target.checked }))
                      }
                    />
                    <strong>Start download at:</strong>
                  </label>
                  <div className="scheduler-time-row">
                    <input
                      type="time"
                      value={activeQueue.startTime}
                      disabled={!activeQueue.startAtEnabled}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({ ...q, startTime: e.target.value }))
                      }
                      className="field-time"
                    />
                    <input
                      type="date"
                      value={activeQueue.startDate}
                      disabled={!activeQueue.startAtEnabled}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({ ...q, startDate: e.target.value }))
                      }
                      className="field-date"
                    />
                  </div>

                  <div className="scheduler-days-row">
                    {DEFAULT_DAYS.map((day, idx) => (
                      <label key={day} className="day-check">
                        <input
                          type="checkbox"
                          checked={activeQueue.days[idx]}
                          disabled={!activeQueue.startAtEnabled}
                          onChange={(e) => {
                            const days = [...activeQueue.days]
                            days[idx] = e.target.checked
                            updateActiveQueue((q) => ({ ...q, days }))
                          }}
                        />
                        <span>{day}</span>
                      </label>
                    ))}
                  </div>
                </div>

                <div className="scheduler-box">
                  <label className="toggle">
                    <input
                      type="checkbox"
                      checked={activeQueue.stopAtEnabled}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({ ...q, stopAtEnabled: e.target.checked }))
                      }
                    />
                    <strong>Stop download at:</strong>
                  </label>
                  <div className="scheduler-time-row">
                    <input
                      type="time"
                      value={activeQueue.stopTime}
                      disabled={!activeQueue.stopAtEnabled}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({ ...q, stopTime: e.target.value }))
                      }
                      className="field-time"
                    />
                  </div>
                </div>

                <div className="scheduler-box">
                  <span className="scheduler-box-label">When queue finishes:</span>
                  <div className="scheduler-options-list">
                    <label className="toggle">
                      <input
                        type="checkbox"
                        checked={activeQueue.turnOffWhenDone}
                        onChange={(e) =>
                          updateActiveQueue((q) => ({ ...q, turnOffWhenDone: e.target.checked }))
                        }
                      />
                      <span>Turn off computer when downloads complete</span>
                    </label>
                    <label className="toggle">
                      <input
                        type="checkbox"
                        checked={activeQueue.exitAppWhenDone}
                        onChange={(e) =>
                          updateActiveQueue((q) => ({ ...q, exitAppWhenDone: e.target.checked }))
                        }
                      />
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
                    value={activeQueue.maxSimultaneous}
                    onChange={(e) =>
                      updateActiveQueue((q) => ({
                        ...q,
                        maxSimultaneous: Math.max(1, Number(e.target.value)),
                      }))
                    }
                    className="field-number"
                  />
                  <span>files at the same time in this queue</span>
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
                      {activeQueue.items.length === 0 ? (
                        <tr>
                          <td colSpan={4} className="queue-empty-td">
                            No files in this queue. Add downloads to queue from the main window.
                          </td>
                        </tr>
                      ) : (
                        activeQueue.items.map((item, index) => (
                          <tr key={item.id}>
                            <td className="queue-order-num">{index + 1}</td>
                            <td className="queue-file-title" title={item.filename}>
                              {item.filename}
                            </td>
                            <td className="queue-file-size">
                              {item.size > 0 ? `${(item.size / 1024 / 1024).toFixed(1)} MB` : '—'}
                            </td>
                            <td>
                              <div className="queue-item-actions">
                                <button
                                  type="button"
                                  className="icon-btn-sm"
                                  disabled={index === 0}
                                  onClick={() => moveItem(index, 'up')}
                                  title="Move Up"
                                >
                                  <MoveUp size={13} />
                                </button>
                                <button
                                  type="button"
                                  className="icon-btn-sm"
                                  disabled={index === activeQueue.items.length - 1}
                                  onClick={() => moveItem(index, 'down')}
                                  title="Move Down"
                                >
                                  <MoveDown size={13} />
                                </button>
                                <button
                                  type="button"
                                  className="icon-btn-sm queue-remove-btn"
                                  onClick={() => removeItem(item.id)}
                                  title="Remove from queue"
                                >
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
                  <input
                    type="checkbox"
                    checked={activeQueue.limitsEnabled}
                    onChange={(e) =>
                      updateActiveQueue((q) => ({ ...q, limitsEnabled: e.target.checked }))
                    }
                  />
                  <strong>Enforce download quota limits</strong>
                </label>

                <p className="field-note">
                  Limit bandwidth usage for this queue to prevent exceeding data caps or slowing
                  down other devices on the network.
                </p>

                <div className="scheduler-box">
                  <div className="scheduler-field-inline">
                    <span>Limit to maximum</span>
                    <input
                      type="number"
                      disabled={!activeQueue.limitsEnabled}
                      value={activeQueue.limitMB}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({
                          ...q,
                          limitMB: Math.max(1, Number(e.target.value)),
                        }))
                      }
                      className="field-number-lg"
                    />
                    <span>Megabytes (MB)</span>
                  </div>

                  <div className="scheduler-field-inline" style={{ marginTop: '10px' }}>
                    <span>Reset counter every</span>
                    <input
                      type="number"
                      disabled={!activeQueue.limitsEnabled}
                      value={activeQueue.limitHours}
                      onChange={(e) =>
                        updateActiveQueue((q) => ({
                          ...q,
                          limitHours: Math.max(1, Number(e.target.value)),
                        }))
                      }
                      className="field-number"
                    />
                    <span>hours</span>
                  </div>
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}
