// Package discordrpc publishes playback to Discord's local Rich Presence RPC.
package discordrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	rpcVersion    = 1
	frameOpcode   = uint32(1)
	handshakeCode = uint32(0)
	closeOpcode   = uint32(2)
	pingOpcode    = uint32(3)
	pongOpcode    = uint32(4)
	maxFrameSize  = 1 << 20
	rpcTimeout    = 3 * time.Second
	retryInterval = 3 * time.Second
	retryCheck    = time.Second
)

var nextNonce atomic.Uint64

// ApplicationID is a Discord application ID accepted by the RPC handshake.
type ApplicationID string

// ParseApplicationID validates the decimal snowflake stored in Settings.
func ParseApplicationID(raw string) (ApplicationID, error) {
	if raw == "" {
		return "", errors.New("discord application ID is empty")
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			return "", errors.New("discord application ID must contain only digits")
		}
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return "", errors.New("discord application ID must be a non-zero decimal number")
	}
	return ApplicationID(raw), nil
}

// State describes the track Discord should show. A state without Playing
// clears the activity; paused playback is intentionally not shared. A zero
// Duration means the length is unknown, so Discord shows only elapsed time.
type State struct {
	VideoID     string
	Title       string
	Artist      string
	ArtworkURL  string
	Position    time.Duration
	Duration    time.Duration
	SeekVersion uint64
	Playing     bool
}

// Session keeps Discord's activity in step with the latest playback state.
// Updates are coalesced so slow IPC never blocks the app's main thread.
type Session struct {
	updates  chan State
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

type dialFunc func(context.Context) (net.Conn, error)

// New opens a background session for a valid Discord application ID.
func New(id ApplicationID) *Session {
	return newSession(id, dialDiscord)
}

func newSession(id ApplicationID, dial dialFunc) *Session {
	s := &Session{updates: make(chan State, 1), stop: make(chan struct{}), done: make(chan struct{})}
	go s.run(id, dial)
	return s
}

// Update replaces any pending state with the latest snapshot.
func (s *Session) Update(state State) {
	select {
	case <-s.stop:
		return
	default:
	}
	select {
	case s.updates <- state:
	default:
		select {
		case <-s.updates:
		default:
		}
		select {
		case s.updates <- state:
		case <-s.stop:
		}
	}
}

// Close clears the current activity and waits for the IPC worker to stop.
func (s *Session) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	<-s.done
}

func (s *Session) run(id ApplicationID, dial dialFunc) {
	defer close(s.done)
	var conn net.Conn
	var reader *rpcReader
	var incoming <-chan rpcReadResult
	var latest State
	var lastSent activityKey
	var sent bool
	var lastStartedAt int64
	retryAt := time.Time{}
	ticker := time.NewTicker(retryCheck)
	defer ticker.Stop()

	closeConnection := func(clear bool) {
		if conn == nil {
			return
		}
		if clear && reader != nil {
			if err := setActivity(conn, reader, nil); err != nil {
				log.Printf("clear Discord Rich Presence: %v", err)
			}
		}
		if reader != nil {
			close(reader.stop)
		}
		_ = conn.Close()
		if reader != nil {
			<-reader.done
		}
		conn, reader, incoming = nil, nil, nil
		sent = false
	}

	update := func(state State) {
		latest = state
		if !state.Playing || state.VideoID == "" {
			closeConnection(true)
			return
		}
		if conn == nil && !time.Now().Before(retryAt) {
			ctx, cancel := context.WithTimeout(context.Background(), rpcTimeout)
			connected, err := connect(ctx, id, dial)
			cancel()
			if err != nil {
				retryAt = time.Now().Add(retryInterval)
				return
			}
			conn = connected
			reader = startRPCReader(conn)
			incoming = reader.events
			sent = false
		}
		if conn == nil {
			return
		}

		key := keyFor(state)
		if sent && key == lastSent {
			return
		}
		position := state.Position
		if position < 0 {
			position = 0
		}
		startedAt := time.Now().Add(-position).Unix()
		if sent && key.VideoID == lastSent.VideoID && key.SeekVersion == lastSent.SeekVersion {
			startedAt = lastStartedAt
		}
		activity := makeActivity(state, startedAt)
		if err := setActivity(conn, reader, activity); err != nil {
			log.Printf("update Discord Rich Presence: %v", err)
			closeConnection(false)
			retryAt = time.Now().Add(retryInterval)
			return
		}
		lastStartedAt = startedAt
		lastSent, sent = key, true
	}

	for {
		select {
		case state := <-s.updates:
			update(state)
		case <-ticker.C:
			if latest.Playing && conn == nil {
				update(latest)
			}
		case result, ok := <-incoming:
			if !ok || result.err != nil {
				if result.err != nil {
					log.Printf("Discord Rich Presence disconnected: %v", result.err)
				}
				closeConnection(false)
				retryAt = time.Now().Add(retryInterval)
			}
		case <-s.stop:
			closeConnection(true)
			return
		}
	}
}

type activityKey struct {
	VideoID     string
	Title       string
	Artist      string
	ArtworkURL  string
	Duration    time.Duration
	SeekVersion uint64
}

// keyFor compares the length in the whole seconds Discord receives, so a more
// precise length for the same track does not resend the activity.
func keyFor(state State) activityKey {
	return activityKey{
		VideoID: state.VideoID, Title: state.Title, Artist: state.Artist,
		ArtworkURL: state.ArtworkURL, Duration: state.Duration.Truncate(time.Second), SeekVersion: state.SeekVersion,
	}
}

type activity struct {
	Type              int         `json:"type"`
	StatusDisplayType int         `json:"status_display_type,omitempty"`
	Details           string      `json:"details,omitempty"`
	State             string      `json:"state,omitempty"`
	Timestamps        *timestamps `json:"timestamps,omitempty"`
	Assets            *assets     `json:"assets,omitempty"`
}

// statusDisplayState makes the member list show the activity's state, the
// artist, instead of the application's name.
const statusDisplayState = 1

type timestamps struct {
	Start int64 `json:"start"`
	End   int64 `json:"end,omitempty"`
}

type assets struct {
	LargeImage string `json:"large_image,omitempty"`
}

func makeActivity(state State, startedAt int64) *activity {
	a := &activity{
		Type:       2,
		Details:    truncate(state.Title, 128),
		State:      truncate(state.Artist, 128),
		Timestamps: &timestamps{Start: startedAt},
	}
	if a.State != "" {
		a.StatusDisplayType = statusDisplayState
	}
	if state.Duration > 0 {
		// Whole seconds, as the player's clock shows the length.
		a.Timestamps.End = startedAt + int64(state.Duration/time.Second)
	}
	if artworkURL := validArtworkURL(state.ArtworkURL); artworkURL != "" {
		a.Assets = &assets{LargeImage: artworkURL}
	}
	return a
}

func validArtworkURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	return truncate(raw, 256)
}

func truncate(text string, limit int) string {
	if len([]rune(text)) <= limit {
		return text
	}
	return string([]rune(text)[:limit])
}

func connect(ctx context.Context, id ApplicationID, dial dialFunc) (net.Conn, error) {
	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	if err := writeFrame(conn, nil, handshakeCode, map[string]any{"v": rpcVersion, "client_id": string(id)}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	for {
		opcode, body, err := readPacket(conn, rpcTimeout)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if opcode == pingOpcode {
			if err := writePacket(conn, nil, pongOpcode, body); err != nil {
				_ = conn.Close()
				return nil, err
			}
			continue
		}
		if opcode != frameOpcode {
			_ = conn.Close()
			return nil, fmt.Errorf("unexpected Discord handshake response opcode %d", opcode)
		}
		var message rpcMessage
		if err := json.Unmarshal(body, &message); err != nil {
			_ = conn.Close()
			return nil, err
		}
		if message.Event == "ERROR" {
			_ = conn.Close()
			return nil, fmt.Errorf("discord RPC handshake error: %s", message.Data)
		}
		if message.Event == "READY" {
			return conn, nil
		}
	}
}

type rpcMessage struct {
	Command string          `json:"cmd"`
	Event   string          `json:"evt"`
	Nonce   string          `json:"nonce"`
	Data    json.RawMessage `json:"data"`
}

type rpcReadResult struct {
	message rpcMessage
	err     error
}

type rpcReader struct {
	events  chan rpcReadResult
	stop    chan struct{}
	done    chan struct{}
	writeMu sync.Mutex
}

func startRPCReader(conn net.Conn) *rpcReader {
	reader := &rpcReader{
		events: make(chan rpcReadResult, 16),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(reader.done)
		defer close(reader.events)
		emit := func(result rpcReadResult) bool {
			select {
			case reader.events <- result:
				return true
			case <-reader.stop:
				return false
			}
		}
		for {
			opcode, body, err := readPacket(conn, 0)
			if err != nil {
				emit(rpcReadResult{err: err})
				return
			}
			switch opcode {
			case pingOpcode:
				if err := writePacket(conn, &reader.writeMu, pongOpcode, body); err != nil {
					emit(rpcReadResult{err: err})
					return
				}
			case pongOpcode:
				continue
			case closeOpcode:
				emit(rpcReadResult{err: errors.New("discord RPC server closed the connection")})
				return
			case frameOpcode:
				var message rpcMessage
				if err := json.Unmarshal(body, &message); err != nil {
					emit(rpcReadResult{err: err})
					return
				}
				if !emit(rpcReadResult{message: message}) {
					return
				}
			default:
				emit(rpcReadResult{err: fmt.Errorf("unexpected Discord RPC opcode %d", opcode)})
				return
			}
		}
	}()
	return reader
}

func setActivity(conn net.Conn, reader *rpcReader, current *activity) error {
	nonce := strconv.Itoa(os.Getpid()) + "-" + strconv.FormatUint(nextNonce.Add(1), 10)
	request := map[string]any{
		"cmd":   "SET_ACTIVITY",
		"args":  map[string]any{"pid": os.Getpid(), "activity": current},
		"nonce": nonce,
	}
	if err := writeFrame(conn, &reader.writeMu, frameOpcode, request); err != nil {
		return err
	}
	timer := time.NewTimer(rpcTimeout)
	defer timer.Stop()
	for {
		var message rpcMessage
		select {
		case result, ok := <-reader.events:
			if !ok {
				return io.EOF
			}
			if result.err != nil {
				return result.err
			}
			message = result.message
		case <-timer.C:
			return errors.New("timed out waiting for discord RPC response")
		}
		if message.Nonce != nonce {
			continue
		}
		if message.Command == "ERROR" || message.Event == "ERROR" {
			return fmt.Errorf("discord RPC error: %s", message.Data)
		}
		return nil
	}
}

func writeFrame(conn net.Conn, writeMu *sync.Mutex, opcode uint32, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) > maxFrameSize {
		return fmt.Errorf("discord RPC frame exceeds %d bytes", maxFrameSize)
	}
	return writePacket(conn, writeMu, opcode, body)
}

func writePacket(conn net.Conn, writeMu *sync.Mutex, opcode uint32, body []byte) error {
	if writeMu != nil {
		writeMu.Lock()
		defer writeMu.Unlock()
	}
	if err := conn.SetWriteDeadline(time.Now().Add(rpcTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	var header [8]byte
	binary.LittleEndian.PutUint32(header[:4], opcode)
	binary.LittleEndian.PutUint32(header[4:], uint32(len(body)))
	if err := writeAll(conn, header[:]); err != nil {
		return err
	}
	return writeAll(conn, body)
}

func readPacket(conn net.Conn, timeout time.Duration) (uint32, []byte, error) {
	if timeout > 0 {
		if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return 0, nil, err
		}
		defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	}
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, nil, err
	}
	opcode, size := binary.LittleEndian.Uint32(header[:4]), binary.LittleEndian.Uint32(header[4:])
	if size > maxFrameSize {
		return 0, nil, fmt.Errorf("discord RPC frame exceeds %d bytes", maxFrameSize)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return 0, nil, err
	}
	return opcode, body, nil
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
