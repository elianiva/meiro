package applog

import "sync/atomic"

type Lazy struct {
	pending string
	value   atomic.Pointer[string]
}

func NewLazy(pending string) *Lazy { return &Lazy{pending: pending} }

func (l *Lazy) Get() string {
	if v := l.value.Load(); v != nil {
		return *v
	}
	return l.pending
}

func (l *Lazy) Start(find func() string, done func(string)) {
	go func() {
		v := find()
		l.value.Store(&v)
		if done != nil {
			done(v)
		}
	}()
}
