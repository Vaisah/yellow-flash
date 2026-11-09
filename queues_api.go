package main

import (
	"errors"

	"github.com/vaisah/internal/application/queues"
	"github.com/vaisah/internal/domain/queue"
)

// The download queues and their schedules, for the UI (the Scheduler).

var errNoQueues = errors.New("queues are not ready")

func (a *App) runner() (*queues.Runner, error) {
	if r := a.queues.Load(); r != nil {
		return r, nil
	}
	return nil, errNoQueues
}

// ListQueues returns every queue in order, with whether it runs.
func (a *App) ListQueues() ([]queue.Queue, error) {
	r, err := a.runner()
	if err != nil {
		return nil, err
	}
	return r.Queues(), nil
}

// CreateQueue adds a queue.
func (a *App) CreateQueue(name string) (queue.Queue, error) {
	r, err := a.runner()
	if err != nil {
		return queue.Queue{}, err
	}
	return r.Create(a.ctx, name)
}

// DeleteQueue removes one of the user's queues; its downloads stay.
func (a *App) DeleteQueue(id string) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Delete(a.ctx, id) })
}

// AddToQueue puts downloads at the end of a queue (out of any other).
func (a *App) AddToQueue(queueID string, downloadIDs []string) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Add(a.ctx, queueID, downloadIDs...) })
}

// RemoveFromQueue takes a download out of its queue.
func (a *App) RemoveFromQueue(downloadID string) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Remove(a.ctx, downloadID) })
}

// MoveInQueue moves a download delta places in its queue.
func (a *App) MoveInQueue(downloadID string, delta int) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Move(a.ctx, downloadID, delta) })
}

// StartQueue runs a queue now.
func (a *App) StartQueue(id string) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Start(a.ctx, id) })
}

// StopQueue halts a queue, pausing its running downloads.
func (a *App) StopQueue(id string) error {
	return a.withRunner(func(r *queues.Runner) error { return r.Stop(a.ctx, id) })
}

// SetQueueFilesAtOnce sets "Download N files at the same time".
func (a *App) SetQueueFilesAtOnce(id string, n int) error {
	return a.withRunner(func(r *queues.Runner) error { return r.SetFilesAtOnce(a.ctx, id, n) })
}

// SetQueueSchedule replaces a queue's schedule.
func (a *App) SetQueueSchedule(id string, s queue.Schedule) error {
	return a.withRunner(func(r *queues.Runner) error { return r.SetSchedule(a.ctx, id, s) })
}

func (a *App) withRunner(fn func(*queues.Runner) error) error {
	r, err := a.runner()
	if err != nil {
		return err
	}
	return fn(r)
}
