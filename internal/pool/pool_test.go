package pool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/iQonAi/agent-task/internal/controller"
)

// The cap holds: with 2 workers, never more than 2 tasks run at once, and a
// 3rd does not start until a slot frees.
func TestConcurrencyCap(t *testing.T) {
	started := make(chan string, 8)
	release := make(chan struct{})
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		started <- req.TaskID
		<-release // hold the slot until the test releases it
		return controller.Outcome{}, nil
	}
	p := New(context.Background(), run, 2, 16)

	for i := 0; i < 5; i++ {
		if err := p.Submit(controller.Request{TaskID: fmt.Sprintf("t%d", i)}); err != nil {
			t.Fatalf("submit: %v", err)
		}
	}

	<-started
	<-started
	// A third must not start while both slots are held.
	select {
	case id := <-started:
		t.Fatalf("task %s started while the cap was full", id)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
}

// Cancel stops a specific running task; its context is cancelled.
func TestCancel(t *testing.T) {
	started := make(chan string, 1)
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		started <- req.TaskID
		<-ctx.Done() // block until cancelled
		return controller.Outcome{}, ctx.Err()
	}
	p := New(context.Background(), run, 2, 8)
	if err := p.Submit(controller.Request{TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	<-started

	if found, queued := p.Cancel("t1"); !found || queued {
		t.Fatalf("Cancel(running) = (%v, %v), want (true, false)", found, queued)
	}
	if found, _ := p.Cancel("nope"); found {
		t.Error("Cancel returned found for an unknown task")
	}
}

// Cancelling a task that is still queued (no free worker yet) tombstones it:
// Cancel reports it as queued and the task never executes.
func TestCancelQueuedSkips(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		started <- req.TaskID
		<-release
		return controller.Outcome{}, nil
	}
	p := New(context.Background(), run, 1, 8)
	if err := p.Submit(controller.Request{TaskID: "running"}); err != nil {
		t.Fatal(err)
	}
	<-started // the single worker is now held
	if err := p.Submit(controller.Request{TaskID: "queued"}); err != nil {
		t.Fatal(err)
	}

	if found, queued := p.Cancel("queued"); !found || !queued {
		t.Fatalf("Cancel(queued) = (%v, %v), want (true, true)", found, queued)
	}

	close(release) // free the worker; it must skip the tombstoned task
	select {
	case id := <-started:
		t.Fatalf("task %s executed after a queued cancel", id)
	case <-time.After(100 * time.Millisecond):
	}
}

// The Submit->pickup race: when Cancel marks a task while it is queued, the
// mark wins — a task reported as queued-cancelled must never execute. Run with
// -race to exercise the locking.
func TestCancelSubmitRace(t *testing.T) {
	var mu sync.Mutex
	ran := make(map[string]bool)
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		mu.Lock()
		ran[req.TaskID] = true
		mu.Unlock()
		return controller.Outcome{}, nil
	}
	p := New(context.Background(), run, 2, 64)

	markedQueued := make([]string, 0, 32)
	for i := 0; i < 32; i++ {
		id := fmt.Sprintf("t%d", i)
		if err := p.Submit(controller.Request{TaskID: id}); err != nil {
			t.Fatalf("submit %s: %v", id, err)
		}
		if found, queued := p.Cancel(id); found && queued {
			markedQueued = append(markedQueued, id)
		}
	}

	// Drain: wait until nothing is pending or running any more.
	deadline := time.After(2 * time.Second)
	for {
		p.mu.Lock()
		idle := len(p.pending) == 0 && len(p.cancels) == 0
		p.mu.Unlock()
		if idle {
			break
		}
		select {
		case <-deadline:
			t.Fatal("pool never drained")
		case <-time.After(5 * time.Millisecond):
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for _, id := range markedQueued {
		if ran[id] {
			t.Errorf("task %s executed although Cancel reported it queued-cancelled", id)
		}
	}
}

// A user cancel carries ErrUserCancel as the context cause; a pool shutdown
// carries ErrShutdown — so recording can tell the two apart (§7.4).
func TestCancelCauses(t *testing.T) {
	causes := make(chan error, 2)
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		<-ctx.Done()
		causes <- context.Cause(ctx)
		return controller.Outcome{}, ctx.Err()
	}

	// User cancel.
	p := New(context.Background(), run, 1, 4)
	if err := p.Submit(controller.Request{TaskID: "user"}); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, p, "user")
	p.Cancel("user")
	if got := <-causes; !errors.Is(got, controller.ErrUserCancel) {
		t.Errorf("user cancel cause = %v, want ErrUserCancel", got)
	}

	// Daemon shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	p2 := New(ctx, run, 1, 4)
	if err := p2.Submit(controller.Request{TaskID: "shut"}); err != nil {
		t.Fatal(err)
	}
	waitRunning(t, p2, "shut")
	cancel()
	if got := <-causes; !errors.Is(got, controller.ErrShutdown) {
		t.Errorf("shutdown cause = %v, want ErrShutdown", got)
	}
}

// waitRunning blocks until taskID has a registered cancel (it is executing).
func waitRunning(t *testing.T, p *Pool, taskID string) {
	t.Helper()
	for range 200 {
		p.mu.Lock()
		_, ok := p.cancels[taskID]
		p.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("task %s never started", taskID)
}

// After shutdown, a worker must not start a job it dequeues; the job is left
// for the next daemon's recovery.
func TestNoNewWorkAfterShutdown(t *testing.T) {
	started := make(chan string, 4)
	release := make(chan struct{})
	run := func(ctx context.Context, req controller.Request) (controller.Outcome, error) {
		started <- req.TaskID
		<-release
		return controller.Outcome{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	p := New(ctx, run, 1, 8)
	if err := p.Submit(controller.Request{TaskID: "running"}); err != nil {
		t.Fatal(err)
	}
	<-started
	if err := p.Submit(controller.Request{TaskID: "queued"}); err != nil {
		t.Fatal(err)
	}

	cancel()       // shutdown begins while "queued" is still in the channel
	close(release) // the running task finishes

	select {
	case id := <-started:
		t.Fatalf("task %s started after shutdown", id)
	case <-time.After(100 * time.Millisecond):
	}
	if !p.WaitTimeout(time.Second) {
		t.Fatal("WaitTimeout timed out on an idle pool")
	}
}

// Cancelling the pool context stops the workers; Wait returns.
func TestWaitOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := New(ctx, func(context.Context, controller.Request) (controller.Outcome, error) {
		return controller.Outcome{}, nil
	}, 2, 8)
	cancel()
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait did not return after context cancel")
	}
}
