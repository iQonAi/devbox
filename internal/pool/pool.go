package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/iQonAi/agent-task/internal/controller"
)

// RunFunc executes one task under a per-task cancellable context.
type RunFunc func(ctx context.Context, req controller.Request) (controller.Outcome, error)

// Pool is a fixed set of workers draining a job queue
type Pool struct {
	run  RunFunc
	jobs chan controller.Request
	ctx  context.Context

	mu        sync.Mutex                         // guards pending, cancelled, cancels
	pending   map[string]bool                    // queued task id -> not yet picked up
	cancelled map[string]bool                    // queued task id -> cancelled before pickup (tombstone)
	cancels   map[string]context.CancelCauseFunc // running task id -> its cancel
	wg        sync.WaitGroup                     // tracks worker goroutines
}

// New starts `workers` goroutines draining a queue of `queue` capacity. The pool
// stops it's workers when ctx is cancelled.
func New(ctx context.Context, run RunFunc, workers, queue int) *Pool {
	if workers < 1 {
		workers = 1
	}

	p := &Pool{
		run:       run,
		jobs:      make(chan controller.Request, queue),
		ctx:       ctx,
		pending:   make(map[string]bool),
		cancelled: make(map[string]bool),
		cancels:   make(map[string]context.CancelCauseFunc),
	}

	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}

	return p
}

// Submit enqueues a task. It never blocks the caller: a full queue is an error,
// not a stall (the caller is the socket handler).
func (p *Pool) Submit(req controller.Request) error {
	p.mu.Lock()
	p.pending[req.TaskID] = true
	p.mu.Unlock()

	select {
	case <-p.ctx.Done():
		p.forget(req.TaskID)
		return fmt.Errorf("pool is shutting down")
	case p.jobs <- req:
		return nil
	default:
		p.forget(req.TaskID)
		return fmt.Errorf("task queue full")
	}
}

// forget drops a task that never made it into the queue.
func (p *Pool) forget(taskID string) {
	p.mu.Lock()
	delete(p.pending, taskID)
	delete(p.cancelled, taskID)
	p.mu.Unlock()
}

// Wait blocks until all workers have exited (after the pool context is
// cancelled). In-flight tasks are cancelled along with the context.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// WaitTimeout waits up to d for the workers to exit. It returns false when the
// drain timed out (a wedged task is still holding a worker).
func (p *Pool) WaitTimeout(d time.Duration) bool {
	done := make(chan struct{})
	go func() { p.wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// worker pulls jobs until the pool context is cancelled.
func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case req := <-p.jobs:
			// The select can pick a queued job even after the pool context was
			// cancelled; never start new work then. The task row stays Created
			// for the next daemon's recovery.
			if p.ctx.Err() != nil {
				return
			}
			p.execute(req)
		}
	}
}

// execute runs one task under a cancellable context registered by task id. A
// task cancelled while still queued (tombstoned) is skipped, never run.
func (p *Pool) execute(req controller.Request) {
	// The task context is detached from the pool context and cancelled by
	// explicit cause only — Cancel uses ErrUserCancel (-> Cancelled), a pool
	// shutdown fires ErrShutdown via AfterFunc (-> Failed). Whichever cancels
	// first sets the cause the recording reads via context.Cause.
	taskCtx, cancel := context.WithCancelCause(context.Background())

	p.mu.Lock()
	delete(p.pending, req.TaskID)
	if p.cancelled[req.TaskID] {
		delete(p.cancelled, req.TaskID)
		p.mu.Unlock()
		cancel(controller.ErrUserCancel)
		return
	}
	p.cancels[req.TaskID] = cancel
	p.mu.Unlock()

	stop := context.AfterFunc(p.ctx, func() { cancel(controller.ErrShutdown) })
	defer func() {
		stop()
		cancel(nil)
		p.mu.Lock()
		delete(p.cancels, req.TaskID)
		p.mu.Unlock()
	}()

	// the run records its own state/events via the controller's recorder
	_, _ = p.run(taskCtx, req)
}

// Cancel signals a task to stop. A running task's context is cancelled; a task
// still in the queue is tombstoned so the worker skips it (a mark always wins
// the Submit->pickup race: both sides act under mu). found is false for an
// unknown task; queued is true when the task was skipped before it ever ran,
// so the caller records the Created->Cancelled transition itself.
func (p *Pool) Cancel(taskID string) (found, queued bool) {
	p.mu.Lock()
	if cancel, ok := p.cancels[taskID]; ok {
		p.mu.Unlock()
		cancel(controller.ErrUserCancel)
		return true, false
	}
	if p.pending[taskID] {
		p.cancelled[taskID] = true
		p.mu.Unlock()
		return true, true
	}
	p.mu.Unlock()
	return false, false
}
