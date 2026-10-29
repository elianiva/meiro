package player

import (
	"errors"
	"io"
	"math"
	"sync/atomic"
	"time"
)

// fadeReader holds back the last hold of a stream, so that when the stream
// ends it can be mixed with the start of the one that follows: the end fades
// out as the next one fades in. It knows nothing of how long the track is; the
// end of the stream is how it finds the end.
//
// It builds up its lookahead as the track plays, taking twice what it hands
// on, so a track starts without waiting for the lookahead to fill. A hold of
// zero passes the stream through.
type fadeReader struct {
	src  io.Reader
	hold atomic.Int64
	// upcoming gives the session that follows, once src has ended cleanly, and
	// nil when none does.
	upcoming func() *session

	buf     []byte
	scratch [32 * 1024]byte
	err     error

	// mixing is set once src has ended and upcoming has been asked. total is
	// how much of the end there was to mix, mixed how much of it has been, and
	// follower the stream it was mixed with.
	mixing   bool
	follower *session
	total    int
	mixed    int
	head     []byte
}

func newFadeReader(src io.Reader, upcoming func() *session) *fadeReader {
	return &fadeReader{src: src, upcoming: upcoming}
}

// setHold changes how much of the end is held back, as a duration of sound.
func (f *fadeReader) setHold(d time.Duration) {
	f.hold.Store(int64(d) * bytesPerSecond / int64(time.Second) / 4 * 4)
}

func (f *fadeReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		if f.err != nil {
			return f.readEnd(p)
		}
		hold := int(f.hold.Load())
		if hold == 0 && len(f.buf) == 0 {
			return f.src.Read(p)
		}
		chunk := f.scratch[:min(len(p), len(f.scratch))]
		n, err := f.src.Read(chunk)
		f.buf = append(f.buf, chunk[:n]...)
		if err != nil {
			f.err = err
			continue
		}
		emit := len(f.buf) - hold
		if emit <= 0 {
			emit = n / 2
		}
		emit = min(emit, len(p))
		if len(p) >= 4 {
			emit -= emit % 4
		}
		if emit <= 0 {
			continue
		}
		copy(p, f.buf[:emit])
		f.buf = f.buf[emit:]
		return emit, nil
	}
}

// readEnd hands on what is left after src has ended: mixed with the start of
// the next stream if there is one, and as it is if not.
func (f *fadeReader) readEnd(p []byte) (int, error) {
	if len(f.buf) == 0 {
		return 0, f.err
	}
	if !f.mixing {
		f.mixing = true
		f.total = len(f.buf)
		if errors.Is(f.err, io.EOF) {
			f.follower = f.upcoming()
		}
	}
	n := min(len(p), len(f.buf)) &^ 3
	if f.follower == nil || n == 0 {
		n = min(len(p), len(f.buf))
		copy(p, f.buf[:n])
		f.buf = f.buf[n:]
		return n, nil
	}
	if cap(f.head) < n {
		f.head = make([]byte, n)
	}
	head := f.head[:n]
	m, _ := io.ReadFull(f.follower.source.reader, head)
	// The follower's own count must include what was taken from it here, so its
	// position starts where the mix left off.
	f.follower.source.read.Add(int64(m))
	m &^= 3
	if m == 0 {
		f.follower = nil
		return f.readEnd(p)
	}
	for i := 0; i < m; i += 2 {
		t := float64(f.mixed+i) / float64(f.total)
		out, in := math.Cos(t*math.Pi/2), math.Sin(t*math.Pi/2)
		a := int16(uint16(f.buf[i]) | uint16(f.buf[i+1])<<8)
		b := int16(uint16(head[i]) | uint16(head[i+1])<<8)
		mix := math.Round(float64(a)*out + float64(b)*in)
		v := uint16(int16(min(max(mix, math.MinInt16), math.MaxInt16)))
		p[i], p[i+1] = byte(v), byte(v>>8)
	}
	f.mixed += m
	f.buf = f.buf[m:]
	return m, nil
}
