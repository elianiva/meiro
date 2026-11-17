package player

import (
	"errors"
	"io"
	"math"
	"sync/atomic"
	"time"
)

type fadeReader struct {
	src      io.Reader
	hold     atomic.Int64
	upcoming func() *session

	buf     []byte
	scratch [32 * 1024]byte
	err     error

	mixing   bool
	follower *session
	total    int
	mixed    int
	head     []byte
}

func newFadeReader(src io.Reader, upcoming func() *session) *fadeReader {
	return &fadeReader{src: src, upcoming: upcoming}
}

func (f *fadeReader) setHold(d time.Duration) {
	f.hold.Store(int64(d) * bytesPerSecond / int64(time.Second) / bytesPerFrame * bytesPerFrame)
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
		if len(p) >= bytesPerFrame {
			emit -= emit % bytesPerFrame
		}
		if emit <= 0 {
			continue
		}
		copy(p, f.buf[:emit])
		f.buf = f.buf[emit:]
		return emit, nil
	}
}

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
	f.follower.source.read.Add(int64(m))
	m -= m % bytesPerFrame
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
