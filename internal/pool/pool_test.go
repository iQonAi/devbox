package pool

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/iQonAi/devbox/internal/controller"
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

	if !p.Cancel("t1") {
		t.Fatal("Cancel returned false for a running task")
	}
	if p.Cancel("nope") {
		t.Error("Cancel returned true for an unknown task")
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
