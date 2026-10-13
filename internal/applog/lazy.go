package applog

import "sync/atomic"

// Lazy is a piece of the log header that takes a while to find, such as a
// tool's version. Until it is found it reads as the pending text, so a file
// started in the meantime still gets a header and the app does not wait.
type Lazy struct {
	pending string
	value   atomic.Pointer[string]
}

// NewLazy makes a Lazy that reads as pending until Start finds the value.
func NewLazy(pending string) *Lazy { return &Lazy{pending: pending} }

// Get is the value if it has been found, else the pending text.
func (l *Lazy) Get() string {
	if v := l.value.Load(); v != nil {
		return *v
	}
	return l.pending
}

// Start finds the value in the background. done, if not nil, is called with
// it once it is found, from that goroutine.
func (l *Lazy) Start(find func() string, done func(string)) {
	go func() {
		v := find()
		l.value.Store(&v)
		if done != nil {
			done(v)
		}
	}()
}
