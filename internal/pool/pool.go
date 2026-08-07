package pool

import (
	"context"
	"fmt"
	"sync"

	"github.com/iQonAi/devbox/internal/controller"
)

// RunFunc executes one task under a per-task cancellable context.
type RunFunc func(ctx context.Context, req controller.Request) (controller.Outcome, error)

// Pool is a fixed set of workers draining a job queue
type Pool struct {
	run  RunFunc
	jobs chan controller.Request
	ctx  context.Context

	mu      sync.Mutex                    // guards cancels
	cancels map[string]context.CancelFunc // running task id -> its cancel
	wg      sync.WaitGroup                // tracks woker goroutines
}

// New starts `workers` goroutines draining a queue of `queue` capacity. The pool
// stops it's workers when ctx is cancelled.
func New(ctx context.Context, run RunFunc, workers, queue int) *Pool {
	if workers < 1 {
		workers = 1
	}

	p := &Pool{
		run:     run,
		jobs:    make(chan controller.Request, queue),
		ctx:     ctx,
		cancels: make(map[string]context.CancelFunc),
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
	select {
	case <-p.ctx.Done():
		return fmt.Errorf("pool is shutting down")
	case p.jobs <- req:
		return nil
	default:
		return fmt.Errorf("task queue full")
	}
}

// Wait blocks until all workers have exited (after the pool context is
// cancelled). In-flight tasks are cancelled along with the context.
func (p *Pool) Wait() {
	p.wg.Wait()
}

// worker pulls jobs until the pool context is cancelled.
func (p *Pool) worker() {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case req := <-p.jobs:
			p.execute(req)
		}
	}
}

// execute runs one task under a cancellable context registred by task id.
func (p *Pool) execute(req controller.Request) {
	taskCtx, cancel := context.WithCancel(p.ctx)
	p.mu.Lock()
	p.cancels[req.TaskID] = cancel
	p.mu.Unlock()

	defer func() {
		cancel()
		p.mu.Lock()
		delete(p.cancels, req.TaskID)
		p.mu.Unlock()
	}()

	// the run records its own state/events via the controller's recorder
	_, _ = p.run(taskCtx, req)
}

// Cancel signals a running task to stop. Returns false if it is not running.
func (p *Pool) Cancel(taskID string) bool {
	p.mu.Lock()
	cancel, ok := p.cancels[taskID]
	p.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
