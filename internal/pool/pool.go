package pool

import (
	"context"
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
