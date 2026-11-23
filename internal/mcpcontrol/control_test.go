package mcpcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLimits(t *testing.T) {
	var l Limiter
	n := time.Unix(1, 0)
	for i := range 10 {
		if l.AllowAfter(n.Add(time.Duration(i)*6*time.Second), 10, 6*time.Second, 1) != 0 {
			t.Fatal(i)
		}
	}
	if l.AllowAfter(n.Add(59*time.Second), 10, 6*time.Second, 1) != time.Second {
		t.Fatal("burst allowed")
	}
	if l.AllowAfter(n.Add(time.Minute), 10, 6*time.Second, 1) != 0 {
		t.Fatal("window did not recover")
	}
}

func TestProtocol(t *testing.T) {
	h := func(context.Context, string, json.RawMessage) (any, error) {
		return map[string]string{"title": "track"}, nil
	}
	s := session{version: "test", handler: h}
	for _, b := range []string{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"now_playing","arguments":{}}}`} {
		r := s.dispatch(t.Context(), []byte(b))
		if !json.Valid(r) || strings.Contains(string(r), `"error"`) {
			t.Fatal(string(r))
		}
	}
	if r := s.dispatch(t.Context(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); r != nil {
		t.Fatal(string(r))
	}
}

func TestIDBudgetIsAtomic(t *testing.T) {
	var l Limiter
	now := time.Unix(1, 0)
	for range 3 {
		if l.AllowAfter(now, 30, 0, 10) != 0 {
			t.Fatal("batch rejected")
		}
	}
	if l.AllowAfter(now, 30, 0, 1) != time.Minute {
		t.Fatal("ID budget exceeded")
	}
	if l.AllowAfter(now.Add(time.Minute), 30, 0, 10) != 0 {
		t.Fatal("budget did not recover")
	}
}

func TestRPCErrorCodes(t *testing.T) {
	for _, test := range []struct {
		request string
		code    int
	}{
		{`{`, -32700},
		{`[]`, -32600},
		{`{"jsonrpc":"1.0","id":1,"method":"ping"}`, -32600},
		{`{"jsonrpc":"2.0","id":true,"method":"ping"}`, -32600},
		{`{"jsonrpc":"2.0","id":1,"method":"server/discover"}`, -32601},
		{`{"jsonrpc":"2.0","id":1,"method":"unknown"}`, -32601},
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":[]}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{}}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"unknown"}}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"cursor":"unknown"}}`, -32602},
		{`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"seek","arguments":null}}`, -32602},
	} {
		s := session{version: "test"}
		if strings.Contains(test.request, `"tools/`) {
			s.protocol = protocolVersion
		}
		result := s.dispatch(t.Context(), []byte(test.request))
		var reply struct{ Error struct{ Code int } }
		if err := json.Unmarshal(result, &reply); err != nil || reply.Error.Code != test.code {
			t.Fatalf("%s: %s", test.request, result)
		}
	}
}

func TestToolCatalogAndNegotiation(t *testing.T) {
	for _, test := range []struct{ requested, expected string }{
		{"2025-06-18", "2025-06-18"},
		{"2099-01-01", protocolVersion},
	} {
		input := fmt.Appendf(nil, `{"jsonrpc":"2.0","id":"init","method":"initialize","params":{"protocolVersion":%q}}`, test.requested)
		s := session{version: "test-version"}
		result := s.dispatch(t.Context(), input)
		var reply struct {
			ID     string
			Result struct {
				ProtocolVersion string
				ServerInfo      map[string]string
				Instructions    string
			}
		}
		if err := json.Unmarshal(result, &reply); err != nil || reply.ID != "init" ||
			reply.Result.ProtocolVersion != test.expected || reply.Result.ServerInfo["version"] != "test-version" ||
			reply.Result.ServerInfo["name"] != "meiro" || reply.Result.Instructions == "" {
			t.Fatalf("initialize: %s", result)
		}
	}
	if len(tools) != 15 {
		t.Fatalf("got %d tools", len(tools))
	}
	for _, tool := range tools {
		if tool.Input["type"] != "object" || tool.Output["type"] != "object" || tool.Description == "" {
			t.Fatalf("missing schema or description for %s", tool.Name)
		}
	}
}

func TestBridgeIPC(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	payload := strings.Repeat("\u65e5\u672c\u8a9e", 16<<10)
	handler := func(ctx context.Context, name string, _ json.RawMessage) (any, error) {
		if name != "queue_list" {
			return nil, errors.New("enable AI control")
		}
		return map[string]any{"value": payload, "number": int64(9007199254740993)}, nil
	}
	server := startServer(ctx, cancel, listener, handler, func() {})
	t.Cleanup(func() { server.Close(); server.Wait() })
	connect := func(ctx context.Context) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
	}
	input := strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"queue_list"}}
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"seek"}}
`)
	var output bytes.Buffer
	if err := bridge(input, &output, "test", connect); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("wanted three replies, got %d", len(lines))
	}
	var reply struct {
		Result struct {
			StructuredContent json.RawMessage
			Content           []struct{ Text string }
		}
	}
	if err := json.Unmarshal([]byte(lines[1]), &reply); err != nil || len(reply.Result.Content) != 1 ||
		string(reply.Result.StructuredContent) != reply.Result.Content[0].Text ||
		!bytes.Contains(reply.Result.StructuredContent, []byte(payload)) ||
		!bytes.Contains(reply.Result.StructuredContent, []byte("9007199254740993")) {
		t.Fatal("large structured result or text fallback differs")
	}
	if !strings.Contains(lines[2], `"isError":true`) || !strings.Contains(lines[2], "enable AI control") {
		t.Fatalf("tool error: %s", lines[2])
	}
}

func TestServerClosesIdleAndActiveClients(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", "127.0.0.1:0")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	started := make(chan struct{})
	changed := make(chan struct{}, 2*connectionLimit)
	server := startServer(ctx, cancel, listener, func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}, func() { changed <- struct{}{} })
	t.Cleanup(func() { server.Close(); server.Wait() })
	for i := range connectionLimit + 1 {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp4", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if i == connectionLimit {
			_ = conn.SetReadDeadline(time.Now().Add(time.Second))
			if _, err := conn.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("extra connection was not closed: %v", err)
			}
			break
		}
		select {
		case <-changed:
		case <-time.After(5 * time.Second):
			t.Fatal("server did not accept client")
		}
		if i == 0 {
			if err := json.NewEncoder(conn).Encode(toolCall{Name: "now_playing"}); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("handler did not start")
			}
		}
	}
	if server.Clients() != connectionLimit {
		t.Fatalf("connected clients: %d", server.Clients())
	}
	server.Close()
	server.Wait()
	if server.Clients() != 0 {
		t.Fatalf("clients left after close: %d", server.Clients())
	}
}

func TestSessionLifecycleAndBatch(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	const list = `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":null}`
	const init = `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{"protocolVersion":%q}}`
	const batch = "[" + ping + "," + `{"jsonrpc":"2.0","method":"ping"}` + "," + list + "]"
	for _, version := range []string{"2024-11-05", "2025-03-26", protocolVersion} {
		s := session{version: "test"}
		send := func(message string) string { return string(s.dispatch(t.Context(), []byte(message))) }
		if got := send(ping); !strings.Contains(got, `"result":{}`) {
			t.Fatal(got)
		}
		if got := send(list); !strings.Contains(got, `"code":-32002`) {
			t.Fatal(got)
		}
		if got := send(fmt.Sprintf(init, version)); !strings.Contains(got, version) {
			t.Fatal(got)
		}
		got := send(list)
		if !strings.Contains(got, `"name":"like"`) || strings.Contains(got, `"outputSchema"`) == s.legacy() ||
			strings.Contains(got, `"annotations"`) == (version == "2024-11-05") {
			t.Fatal(got)
		}
		got = send(batch)
		if s.legacy() {
			var replies []json.RawMessage
			if err := json.Unmarshal([]byte(got), &replies); err != nil || len(replies) != 2 {
				t.Fatal(got)
			}
		} else if !strings.Contains(got, `"code":-32600`) {
			t.Fatal(got)
		}
	}
}

func TestBridgeFraming(t *testing.T) {
	const ping = `{"jsonrpc":"2.0","id":9,"method":"ping"}`
	exact := strings.Repeat(" ", requestLimit-len(ping)) + ping
	input := strings.NewReader("\n " + "\n" + exact + "\r\n" + strings.Repeat("x", requestLimit*2) + "\n" + ping)
	var output bytes.Buffer
	if err := bridge(input, &output, "test", nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], `"id":9`) ||
		!strings.Contains(lines[1], "exceeds 64 KiB") || !strings.Contains(lines[2], `"id":9`) {
		t.Fatal(output.String())
	}
}

func TestIPCErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		hint string
	}{
		{os.ErrNotExist, "start Meiro"},
		{errors.New("bad socket location"), "bad socket location"},
	} {
		_, err := callIPC(t.Context(), func(context.Context) (net.Conn, error) { return nil, test.err }, "now_playing", nil)
		if err == nil || !strings.Contains(err.Error(), test.hint) {
			t.Fatalf("%v", err)
		}
	}
	if got := ipcError(os.ErrDeadlineExceeded); !strings.Contains(got.Error(), "in time") {
		t.Fatal(got)
	}
	if got := ipcError(nil); !strings.Contains(got.Error(), "closed") {
		t.Fatal(got)
	}
}

type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, errors.New("test accept failure") }
func (failingListener) Close() error              { return nil }

func TestListenerReportsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	changed := make(chan struct{}, 1)
	server := startServer(ctx, cancel, failingListener{}, nil, func() { changed <- struct{}{} })
	t.Cleanup(func() { server.Close(); server.Wait() })
	select {
	case <-changed:
		if got := server.Failure(); got != "test accept failure" {
			t.Fatal(got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("missing listener error")
	}
}
