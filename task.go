package main

import (
	"context"
	"sync/atomic"
	"time"
)

type task struct {
	_      noCopy
	gen    uint64
	cancel context.CancelFunc
}

var generation atomic.Uint64

func (t *task) stop() {
	t.gen = generation.Add(1)
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

func (t *task) busy() bool { return t.cancel != nil }

const noTimeout time.Duration = 0

func startTask[T any](a *app, t *task, timeout time.Duration, work func(ctx context.Context) (T, error), apply func(T, error)) {
	startProgress(a, t, timeout, func(ctx context.Context, _ func(func())) (T, error) {
		return work(ctx)
	}, apply)
}

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
				return
			}
			cancel()
			t.cancel = nil
			apply(result, err)
		})
	})
}

func spawn[T any](a *app, work func() T, apply func(T)) {
	a.run(func() {
		result := work()
		a.update(func() { apply(result) })
	})
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}
