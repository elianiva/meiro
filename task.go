package main

import (
	"context"
	"sync/atomic"
	"time"
)

// task is the slot for one piece of background work whose result the main
// thread applies, such as the load of the page shown or the lyrics of the
// track playing. Starting the task again, or stopping it, supersedes the
// work in flight: its context is cancelled and its result is dropped without
// being applied.
//
// Every piece of work whose answer can go stale goes through a task, so no
// caller compares generations by hand, and none can forget to.
//
// A task belongs to the main thread: start, stop and busy are called there,
// and results are applied there. A task must not be copied, which go vet
// enforces: a copy would share its work in flight with the original.
type task struct {
	_      noCopy
	gen    uint64
	cancel context.CancelFunc
}

// generation numbers every start and stop of every task, so no two pieces of
// work ever share one: even a task reset to its zero value cannot mistake
// the work of the one it replaced for its own.
var generation atomic.Uint64

// stop cancels the work in flight, if any, and drops its result.
func (t *task) stop() {
	t.gen = generation.Add(1)
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

// busy reports whether work is in flight.
func (t *task) busy() bool { return t.cancel != nil }

// noTimeout is the timeout of work that runs for as long as it needs, until
// it is superseded: a page that streams in, whose requests bound themselves.
const noTimeout time.Duration = 0

// startTask runs work off the main thread, with a context that ends after
// timeout, if it is not noTimeout, or when the task is superseded, and then
// runs apply on the main thread with what work returned, unless the task was
// started again or stopped meanwhile.
func startTask[T any](a *app, t *task, timeout time.Duration, work func(ctx context.Context) (T, error), apply func(T, error)) {
	startProgress(a, t, timeout, func(ctx context.Context, _ func(func())) (T, error) {
		return work(ctx)
	}, apply)
}

// startProgress is startTask for work that shows parts of its result as it
// goes: progress runs fn on the main thread, while the task is still the one
// that started the work.
func startProgress[T any](a *app, t *task, timeout time.Duration, work func(ctx context.Context, progress func(fn func())) (T, error), apply func(T, error)) {
	t.stop()
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout == noTimeout {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	t.cancel = cancel
	gen := t.gen
	current := func() bool { return t.gen == gen }
	progress := func(fn func()) {
		a.update(func() {
			if current() {
				fn()
			}
		})
	}
	a.run(func() {
		result, err := work(ctx, progress)
		a.update(func() {
			if !current() {
				return // superseded: stop already cancelled the context
			}
			cancel()
			t.cancel = nil
			apply(result, err)
		})
	})
}

// spawn runs work off the main thread and apply with its result on the main
// thread, always. It is for work whose result cannot go stale, such as a
// write the user asked for; work that can go stale goes through a task.
func spawn[T any](a *app, work func() T, apply func(T)) {
	a.run(func() {
		result := work()
		a.update(func() { apply(result) })
	})
}

// noCopy makes go vet report copies of the struct that holds it.
type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
