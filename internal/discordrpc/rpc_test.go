package discordrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseApplicationID(t *testing.T) {
	for _, tt := range []struct {
		name string
		id   string
		ok   bool
	}{
		{name: "decimal snowflake", id: "123456789012345678", ok: true},
		{name: "empty", id: ""},
		{name: "non-decimal", id: "123abc"},
		{name: "zero", id: "0"},
		{name: "too large", id: "18446744073709551616"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseApplicationID(tt.id)
			if (err == nil) != tt.ok {
				t.Fatalf("ParseApplicationID(%q) = %q, %v; valid = %t", tt.id, got, err, tt.ok)
			}
			if tt.ok && string(got) != tt.id {
				t.Errorf("application ID = %q, want %q", got, tt.id)
			}
		})
	}
}

func TestSessionSendsListeningActivityAndClearsOnPause(t *testing.T) {
	id, err := ParseApplicationID("123456789012345678")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 1)
	session := newSession(id, func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		connections <- server
		return client, nil
	})
	defer session.Close()
	session.Update(State{
		VideoID: "track-1", Title: "Song", Artist: "Artist", ArtworkURL: "https://i.ytimg.com/cover.jpg",
		Position: 42 * time.Second, Playing: true,
	})

	server := <-connections
	defer server.Close()
	message, opcode := readTestMessage(t, server)
	if opcode != handshakeCode {
		t.Fatalf("handshake opcode = %d, want %d", opcode, handshakeCode)
	}
	var handshake struct {
		Version  int    `json:"v"`
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(message, &handshake); err != nil {
		t.Fatal(err)
	}
	if handshake.Version != rpcVersion || handshake.ClientID != string(id) {
		t.Fatalf("handshake = %+v", handshake)
	}
	writeTestMessage(t, server, frameOpcode, `{"cmd":"DISPATCH","evt":"READY","data":{}}`)

	request := readActivityRequest(t, server)
	if err := replyToActivity(server, request.Nonce); err != nil {
		t.Fatal(err)
	}
	var got activity
	if err := json.Unmarshal(request.Activity, &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != 2 || got.Details != "Song" || got.State != "Artist" {
		t.Errorf("activity = %+v", got)
	}
	if got.StatusDisplayType != statusDisplayState {
		t.Errorf("status display type = %d, want %d so the member list names the artist", got.StatusDisplayType, statusDisplayState)
	}
	if got.Assets == nil || got.Assets.LargeImage != "https://i.ytimg.com/cover.jpg" {
		t.Errorf("artwork = %+v", got.Assets)
	}
	if strings.Contains(string(request.Activity), `"large_text"`) {
		t.Errorf("activity repeats the track title in artwork hover text: %s", request.Activity)
	}
	if got.Timestamps == nil || time.Until(time.Unix(got.Timestamps.Start, 0)) > -41*time.Second {
		t.Errorf("elapsed timestamp = %+v, want about 42 seconds ago", got.Timestamps)
	}

	session.Update(State{
		VideoID: "track-1", Title: "Song", Artist: "Artist", ArtworkURL: "https://i.ytimg.com/cover.jpg",
		Position: 43 * time.Second, Playing: true,
	})
	if err := server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestMessageRaw(server); err == nil {
		t.Fatal("same track update unexpectedly sent another activity")
	}
	if err := server.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	session.Update(State{VideoID: "track-1"})
	cleared := readActivityRequest(t, server)
	if string(cleared.Activity) != "null" {
		t.Errorf("pause activity = %s, want null", cleared.Activity)
	}
	if err := replyToActivity(server, cleared.Nonce); err != nil {
		t.Fatal(err)
	}
}

func TestSessionAddsTrackLengthWhenItBecomesKnown(t *testing.T) {
	id, err := ParseApplicationID("123456789012345678")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 1)
	session := newSession(id, func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		connections <- server
		return client, nil
	})
	defer session.Close()
	session.Update(State{VideoID: "track-1", Title: "Song", Position: 42 * time.Second, Playing: true})

	server := <-connections
	defer server.Close()
	acceptTestHandshake(t, server)
	first := readActivity(t, server)
	if first.Timestamps == nil || first.Timestamps.End != 0 {
		t.Fatalf("timestamps before the length is known = %+v, want a start and no end", first.Timestamps)
	}

	session.Update(State{
		VideoID: "track-1", Title: "Song", Position: 43 * time.Second,
		Duration: 3*time.Minute + 30*time.Second + 700*time.Millisecond, Playing: true,
	})
	second := readActivity(t, server)
	if second.Timestamps == nil || second.Timestamps.Start != first.Timestamps.Start {
		t.Fatalf("timestamps once the length is known = %+v, want the start kept at %d", second.Timestamps, first.Timestamps.Start)
	}
	if second.Timestamps.End != first.Timestamps.Start+210 {
		t.Errorf("end = %d, want start + 210 seconds, the length the player shows (%d)", second.Timestamps.End, first.Timestamps.Start+210)
	}
}

func TestSessionAnswersPingWhileIdle(t *testing.T) {
	id, err := ParseApplicationID("123456789012345678")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 1)
	session := newSession(id, func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		connections <- server
		return client, nil
	})
	defer session.Close()
	session.Update(State{VideoID: "track-1", Playing: true})

	server := <-connections
	defer server.Close()
	if _, opcode := readTestMessage(t, server); opcode != handshakeCode {
		t.Fatalf("handshake opcode = %d, want %d", opcode, handshakeCode)
	}
	if err := writeTestPacket(server, frameOpcode, []byte(`{"evt":"READY"}`)); err != nil {
		t.Fatal(err)
	}
	request := readActivityRequest(t, server)
	if err := replyToActivity(server, request.Nonce); err != nil {
		t.Fatal(err)
	}

	const pingOpcode, pongOpcode = uint32(3), uint32(4)
	ping := []byte("discord-ping")
	writeErr := make(chan error, 1)
	go func() { writeErr <- writeTestPacket(server, pingOpcode, ping) }()
	if err := server.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	got, opcode, err := readTestMessageRaw(server)
	if err != nil {
		t.Fatalf("read PONG: %v", err)
	}
	if opcode != pongOpcode || string(got) != string(ping) {
		t.Fatalf("PONG = opcode %d, payload %q; want opcode %d, payload %q", opcode, got, pongOpcode, ping)
	}
	if err := <-writeErr; err != nil {
		t.Fatal(err)
	}
}

func TestSessionReconnectsAfterDiscordClosesIPC(t *testing.T) {
	id, err := ParseApplicationID("123456789012345678")
	if err != nil {
		t.Fatal(err)
	}
	connections := make(chan net.Conn, 2)
	session := newSession(id, func(context.Context) (net.Conn, error) {
		client, server := net.Pipe()
		connections <- server
		return client, nil
	})
	defer session.Close()
	session.Update(State{VideoID: "track-1", Title: "Song", Playing: true})

	first := <-connections
	defer first.Close()
	acceptTestHandshake(t, first)
	request := readActivityRequest(t, first)
	if err := replyToActivity(first, request.Nonce); err != nil {
		t.Fatal(err)
	}
	if err := writeTestPacket(first, closeOpcode, []byte(`{"code":1000}`)); err != nil {
		t.Fatal(err)
	}

	var second net.Conn
	select {
	case second = <-connections:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not reconnect after Discord closed IPC")
	}
	defer second.Close()
	acceptTestHandshake(t, second)
	request = readActivityRequest(t, second)
	if err := replyToActivity(second, request.Nonce); err != nil {
		t.Fatal(err)
	}
	var got activity
	if err := json.Unmarshal(request.Activity, &got); err != nil {
		t.Fatal(err)
	}
	if got.Details != "Song" {
		t.Fatalf("reconnected activity = %+v", got)
	}

	closed := make(chan struct{})
	go func() {
		session.Close()
		close(closed)
	}()
	cleared := readActivityRequest(t, second)
	if string(cleared.Activity) != "null" {
		t.Errorf("shutdown activity = %s, want null", cleared.Activity)
	}
	if err := replyToActivity(second, cleared.Nonce); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("session did not close after clearing activity")
	}
}

func acceptTestHandshake(t *testing.T, server net.Conn) {
	t.Helper()
	if _, opcode := readTestMessage(t, server); opcode != handshakeCode {
		t.Fatalf("handshake opcode = %d, want %d", opcode, handshakeCode)
	}
	if err := writeTestPacket(server, frameOpcode, []byte(`{"evt":"READY"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestMakeActivityBoundsFieldsAndRejectsNonHTTPSArtwork(t *testing.T) {
	title := strings.Repeat("界", 129)
	got := makeActivity(State{Title: title, ArtworkURL: "http://example.com/cover.jpg"}, 123)
	if len([]rune(got.Details)) != 128 {
		t.Errorf("details length = %d, want 128 runes", len([]rune(got.Details)))
	}
	if got.Assets != nil {
		t.Errorf("non-HTTPS artwork was included: %+v", got.Assets)
	}
	if got.StatusDisplayType != 0 {
		t.Errorf("status display type without an artist = %d, want the application's name", got.StatusDisplayType)
	}
}

type activityRequest struct {
	Nonce    string
	Activity json.RawMessage
}

func readActivityRequest(t *testing.T, conn net.Conn) activityRequest {
	t.Helper()
	message, opcode := readTestMessage(t, conn)
	if opcode != frameOpcode {
		t.Fatalf("activity opcode = %d, want %d", opcode, frameOpcode)
	}
	var request struct {
		Command string `json:"cmd"`
		Nonce   string `json:"nonce"`
		Args    struct {
			Activity json.RawMessage `json:"activity"`
		} `json:"args"`
	}
	if err := json.Unmarshal(message, &request); err != nil {
		t.Fatal(err)
	}
	if request.Command != "SET_ACTIVITY" || request.Nonce == "" {
		t.Fatalf("activity request = %+v", request)
	}
	return activityRequest{Nonce: request.Nonce, Activity: request.Args.Activity}
}

func readActivity(t *testing.T, conn net.Conn) activity {
	t.Helper()
	request := readActivityRequest(t, conn)
	if err := replyToActivity(conn, request.Nonce); err != nil {
		t.Fatal(err)
	}
	var got activity
	if err := json.Unmarshal(request.Activity, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func replyToActivity(conn net.Conn, nonce string) error {
	body, err := json.Marshal(map[string]any{"cmd": "SET_ACTIVITY", "data": nil, "nonce": nonce})
	if err != nil {
		return err
	}
	var header [8]byte
	binary.LittleEndian.PutUint32(header[:4], frameOpcode)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	_, err = conn.Write(body)
	return err
}

func writeTestMessage(t *testing.T, conn net.Conn, opcode uint32, body string) {
	t.Helper()
	if err := writeTestPacket(conn, opcode, []byte(body)); err != nil {
		t.Fatal(err)
	}
}

func writeTestPacket(conn net.Conn, opcode uint32, body []byte) error {
	var header [8]byte
	binary.LittleEndian.PutUint32(header[:4], opcode)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
	if _, err := conn.Write(header[:]); err != nil {
		return err
	}
	_, err := conn.Write(body)
	return err
}

func readTestMessage(t *testing.T, conn net.Conn) ([]byte, uint32) {
	t.Helper()
	body, opcode, err := readTestMessageRaw(conn)
	if err != nil {
		t.Fatal(err)
	}
	return body, opcode
}

func readTestMessageRaw(conn net.Conn) ([]byte, uint32, error) {
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, 0, err
	}
	opcode, size := binary.LittleEndian.Uint32(header[:4]), binary.LittleEndian.Uint32(header[4:])
	body := make([]byte, size)
	_, err := io.ReadFull(conn, body)
	return body, opcode, err
}
