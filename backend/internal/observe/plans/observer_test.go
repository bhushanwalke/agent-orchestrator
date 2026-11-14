package plans

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeScheduler struct {
	mu    sync.Mutex
	ticks int
	wake  chan struct{}
}

func (f *fakeScheduler) Tick(context.Context, time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ticks++
	return nil
}

func (f *fakeScheduler) WakeC() <-chan struct{} { return f.wake }

func (f *fakeScheduler) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ticks
}

// A wake runs a tick without waiting for the interval.
func TestObserverTicksOnStartAndOnWake(t *testing.T) {
	sched := &fakeScheduler{wake: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := New(sched, Config{Tick: time.Hour}).Start(ctx)
	waitFor(t, func() bool { return sched.count() == 1 })
	sched.wake <- struct{}{}
	waitFor(t, func() bool { return sched.count() == 2 })
	cancel()
	<-done
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met")
}
