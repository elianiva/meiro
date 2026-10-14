package player

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStreamArgsProtocolWhitelist(t *testing.T) {
	args := streamArgs("https://media.test/audio", 0)
	index := slices.Index(args, "-protocol_whitelist")
	if index < 0 || index+1 == len(args) {
		t.Fatalf("no protocol whitelist in %v", args)
	}
	if slices.Contains(strings.Split(args[index+1], ","), "file") {
		t.Fatalf("remote stream may read local files: %q", args[index+1])
	}
}

func TestStreamArgsLocalFilesHaveNoWhitelist(t *testing.T) {
	if args := streamArgs("/tmp/audio.wav", 0); slices.Contains(args, "-protocol_whitelist") {
		t.Fatalf("local file got a whitelist that would block it: %v", args)
	}
}

func TestStreamArgsDoesNotAddHTTPOptionsToLocalFiles(t *testing.T) {
	args := streamArgs("/tmp/audio.wav", 0)
	if slices.Contains(args, "-reconnect") || slices.Contains(args, "-user_agent") {
		t.Fatalf("local file got HTTP-only options: %v", args)
	}
}

func TestReapObservesExitWhileReaderIsPaused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses sh")
	}
	p := &Player{}
	cmd := exec.Command("sh", "-c", "exit 7")
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	s := &session{cmd: cmd, pipe: pipe, done: make(chan struct{}), quit: make(chan struct{}), source: newCountingReader(pipe, nil)}
	go p.reap(s)
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("process exit was not observed while the reader was idle")
	}
	close(s.quit)
}

func TestCleanExitWithUnreadOutputIsNotAFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test command uses sh")
	}
	p := &Player{}
	cmd := exec.Command("sh", "-c", "printf abcd")
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	s := &session{
		cmd: cmd, pipe: pipe, stderr: &boundedBuffer{limit: 16},
		done: make(chan struct{}), quit: make(chan struct{}), source: newCountingReader(pipe, nil),
	}
	go p.reap(s)
	<-s.done
	if !p.pendingSamples(s) {
		t.Fatal("clean exit with unread output must not read as a failed stream")
	}
	data, err := io.ReadAll(s.source)
	if err != nil || string(data) != "abcd" {
		t.Fatalf("read %q, %v: output lost after exit", data, err)
	}
	if p.pendingSamples(s) {
		t.Fatal("nothing is pending once the reader has the data")
	}
}
