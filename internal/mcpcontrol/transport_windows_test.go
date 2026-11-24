//go:build windows

package mcpcontrol

import (
	"strconv"
	"testing"
	"time"
)

func TestPipeLifecycle(t *testing.T) {
	path, sid, err := endpoint()
	if err != nil || path != `\\.\pipe\meiro-mcp-`+sid || sid == "" {
		t.Fatalf("endpoint: %q %q %v", path, sid, err)
	}
	path += "-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	listener, err := listenPipe(path, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if second, err := listenPipe(path, sid); err == nil {
		_ = second.Close()
		t.Fatal("second listener opened")
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			defer conn.Close()
			var data [1]byte
			_, err = conn.Read(data[:])
		}
		accepted <- err
	}()
	conn, err := dialPipe(t.Context(), path, sid)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := verifyPipeOwner(conn, "S-1-0-0"); err == nil {
		t.Fatal("accepted a different SID")
	}
	if _, err := conn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}
