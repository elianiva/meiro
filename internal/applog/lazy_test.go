package applog

import "testing"

func TestLazyReadsPendingUntilFound(t *testing.T) {
	l := NewLazy("looking up")
	if got := l.Get(); got != "looking up" {
		t.Fatalf("before = %q", got)
	}
	release, found := make(chan struct{}), make(chan string)
	l.Start(func() string { <-release; return "ffmpeg 7.1" }, func(v string) { found <- v })
	if got := l.Get(); got != "looking up" {
		t.Fatalf("while looking = %q", got)
	}
	close(release)
	if v := <-found; v != "ffmpeg 7.1" {
		t.Fatalf("done got %q", v)
	}
	if got := l.Get(); got != "ffmpeg 7.1" {
		t.Fatalf("after = %q", got)
	}
}
