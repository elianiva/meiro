package cookies

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // the WebSocket handshake is defined with SHA-1
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// This is the smallest WebSocket client the browser's debugging port needs:
// text messages over a loopback connection, with no extensions. It is not a
// general client.

const (
	wsGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsMaxMessage = 8 << 20

	opContinuation = 0x0
	opText         = 0x1
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

type wsConn struct {
	conn net.Conn
	r    *bufio.Reader
	// client frames are masked, server frames are not.
	client bool
}

// dialWS opens a WebSocket to a loopback address. ctx bounds the handshake
// and ends the connection when it is done.
func dialWS(ctx context.Context, addr, path string) (*wsConn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	ws := &wsConn{conn: conn, r: bufio.NewReader(conn), client: true}
	// The connection ends with ctx, so a cancelled sign-in cannot leave a
	// read waiting.
	context.AfterFunc(ctx, func() { conn.Close() })
	if err := ws.handshake(ctx, addr, path); err != nil {
		conn.Close()
		return nil, err
	}
	return ws, nil
}

func (w *wsConn) handshake(ctx context.Context, addr, path string) error {
	defer w.limit(ctx)()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(nonce)
	_, err := fmt.Fprintf(w.conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, addr, key)
	if err != nil {
		return err
	}
	resp, err := http.ReadResponse(w.r, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("browser refused the debugging connection: %s", resp.Status)
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != wsAccept(key) {
		return errors.New("browser answered the debugging handshake wrongly")
	}
	return nil
}

func wsAccept(key string) string {
	sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // defined by RFC 6455
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (w *wsConn) Close() error { return w.conn.Close() }

// limit holds reads and writes to ctx's deadline, and returns what lifts the
// limit again. A deadline that cannot be set leaves the connection to ctx's
// cancellation, which closes it.
func (w *wsConn) limit(ctx context.Context) (lift func()) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return func() {}
	}
	_ = w.conn.SetDeadline(deadline)
	return func() { _ = w.conn.SetDeadline(time.Time{}) }
}

// writeText sends one text message.
func (w *wsConn) writeText(payload []byte) error { return w.writeFrame(opText, payload) }

func (w *wsConn) writeFrame(op byte, payload []byte) error {
	header := []byte{0x80 | op, 0}
	switch n := len(payload); {
	case n < 126:
		header[1] = byte(n)
	case n <= 0xFFFF:
		header[1] = 126
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header[1] = 127
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}
	if w.client {
		header[1] |= 0x80
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		header = append(header, mask[:]...)
		masked := make([]byte, len(payload))
		for i, b := range payload {
			masked[i] = b ^ mask[i%4]
		}
		payload = masked
	}
	_, err := w.conn.Write(append(header, payload...))
	return err
}

// readMessage reads the next text message, answering pings on the way. It
// returns io.EOF when the peer closes.
func (w *wsConn) readMessage() ([]byte, error) {
	var message []byte
	for {
		var head [2]byte
		if _, err := io.ReadFull(w.r, head[:]); err != nil {
			return nil, err
		}
		fin, op := head[0]&0x80 != 0, head[0]&0x0F
		masked, size := head[1]&0x80 != 0, uint64(head[1]&0x7F)
		switch size {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return nil, err
			}
			size = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.r, b[:]); err != nil {
				return nil, err
			}
			size = binary.BigEndian.Uint64(b[:])
		}
		if size > wsMaxMessage || uint64(len(message))+size > wsMaxMessage {
			return nil, errors.New("debugging message is too large")
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(w.r, mask[:]); err != nil {
				return nil, err
			}
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(w.r, payload); err != nil {
			return nil, err
		}
		if masked {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case opClose:
			return nil, io.EOF
		case opPing:
			if err := w.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
		case opText, opContinuation:
			message = append(message, payload...)
			if fin {
				return message, nil
			}
		}
	}
}
