package memprof

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"time"
)

const Env = "MEIRO_MEMPROF"

func Start(interval time.Duration) (stop func()) {
	dir := os.Getenv(Env)
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("memprof: %v", err)
		return func() {}
	}
	logf, err := os.OpenFile(filepath.Join(dir, "memstats.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Printf("memprof: %v", err)
		return func() {}
	}
	runtime.MemProfileRate = 64 << 10
	start := time.Now()
	n := 0
	sample := func(final bool) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		mb := func(b uint64) float64 { return float64(b) / (1 << 20) }
		fmt.Fprintf(logf, "t=%s heapAlloc=%.1fMB heapInuse=%.1fMB heapIdle=%.1fMB heapReleased=%.1fMB stack=%.1fMB sys=%.1fMB objects=%d numGC=%d goroutines=%d\n",
			time.Since(start).Round(time.Second), mb(m.HeapAlloc), mb(m.HeapInuse), mb(m.HeapIdle), mb(m.HeapReleased), mb(m.StackInuse), mb(m.Sys), m.HeapObjects, m.NumGC, runtime.NumGoroutine())
		name := fmt.Sprintf("heap-%03d.pprof", n)
		if final {
			name = "heap-final.pprof"
		}
		n++
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return
		}
		defer f.Close()
		_ = pprof.Lookup("heap").WriteTo(f, 0)
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				sample(false)
			case <-done:
				sample(true)
				logf.Close()
				return
			}
		}
	}()
	log.Printf("memprof: writing to %s every %s", dir, interval)
	return func() { close(done); <-finished }
}
