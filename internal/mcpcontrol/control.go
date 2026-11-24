package mcpcontrol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	requestLimit    = 64 << 10
	resultLimit     = 1 << 20
	connectionLimit = 4
	callTimeout     = 30 * time.Second
)

type Handler func(context.Context, string, json.RawMessage) (any, error)

type Server struct {
	cancel   context.CancelFunc
	listener net.Listener
	workers  sync.WaitGroup
	once     sync.Once
	clients  atomic.Int32
	failure  atomic.Pointer[string]
}

func Start(handler Handler, changed func()) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())
	listener, err := listen(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	return startServer(ctx, cancel, listener, handler, changed), nil
}

func startServer(ctx context.Context, cancel context.CancelFunc, listener net.Listener, handler Handler, changed func()) *Server {
	server := &Server{listener: listener, cancel: cancel}
	server.workers.Go(func() { server.serve(ctx, handler, changed) })
	return server
}

func (s *Server) Close() {
	s.once.Do(func() {
		s.cancel()
		_ = s.listener.Close()
	})
}

func (s *Server) Wait() { s.workers.Wait() }

func (s *Server) Clients() int { return int(s.clients.Load()) }

func (s *Server) Failure() string {
	if failure := s.failure.Load(); failure != nil {
		return *failure
	}
	return ""
}

func (s *Server) serve(ctx context.Context, handler Handler, changed func()) {
	var failures int
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("accept local MCP connection: %v", err)
			failures++
			if failures >= 3 || errors.Is(err, net.ErrClosed) {
				message := err.Error()
				s.failure.Store(&message)
				s.cancel()
				_ = s.listener.Close()
				changed()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		failures = 0
		if s.clients.Load() >= connectionLimit {
			_ = conn.Close()
			continue
		}
		s.clients.Add(1)
		changed()
		s.workers.Go(func() {
			defer func() {
				s.clients.Add(-1)
				if ctx.Err() == nil {
					changed()
				}
			}()
			defer conn.Close()
			s.serveClient(ctx, conn, handler)
		})
	}
}

func (s *Server) serveClient(ctx context.Context, conn net.Conn, handler Handler) {
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 4096), requestLimit+1)
	if scan.Scan() {
		var request toolCall
		var response ipcResponse
		if err := json.Unmarshal(scan.Bytes(), &request); err != nil || request.Name == "" {
			response.Error = "invalid Meiro tool request"
		} else {
			response = runTool(ctx, request, handler)
		}
		_ = conn.SetWriteDeadline(time.Now().Add(callTimeout))
		if err := json.NewEncoder(conn).Encode(response); err != nil {
			return
		}
	}
}

type toolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

type ipcResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func runTool(ctx context.Context, request toolCall, handler Handler) ipcResponse {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	result, err := handler(ctx, request.Name, request.Arguments)
	if err != nil {
		return ipcResponse{Error: err.Error()}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return ipcResponse{Error: "could not encode the Meiro tool result"}
	}
	return ipcResponse{Result: data}
}

func callIPC(ctx context.Context, connect func(context.Context) (net.Conn, error), name string, args json.RawMessage) (any, error) {
	data, err := json.Marshal(toolCall{Name: name, Arguments: args})
	if err != nil || len(data) > requestLimit {
		return nil, errors.New("tool request exceeds Meiro’s 64 KiB limit")
	}
	conn, err := connect(ctx)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil, errors.New("start Meiro and enable Settings > Desktop > AI control")
		}
		return nil, fmt.Errorf("connect to Meiro: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(callTimeout + time.Second))
	_, err = conn.Write(append(data, '\n'))
	if err != nil {
		return nil, ipcError(err)
	}
	scan := bufio.NewScanner(conn)
	scan.Buffer(make([]byte, 4096), resultLimit)
	if !scan.Scan() {
		return nil, ipcError(scan.Err())
	}
	var response ipcResponse
	if err := json.Unmarshal(scan.Bytes(), &response); err != nil {
		return nil, errors.New("invalid Meiro reply")
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return response.Result, nil
}

func ipcError(err error) error {
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return errors.New("no reply from Meiro in time; read the current state before retrying")
	}
	return errors.New("the Meiro connection closed; check AI control and read the current state before retrying")
}

func Bridge(in io.Reader, out io.Writer, version string) error {
	return bridge(in, out, version, dial)
}

func bridge(in io.Reader, out io.Writer, version string, connect func(context.Context) (net.Conn, error)) error {
	session := session{version: version, handler: func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		return callIPC(ctx, connect, name, args)
	}}
	reader := bufio.NewReaderSize(in, requestLimit+2)
	for {
		line, err := reader.ReadSlice('\n')
		tooLong := errors.Is(err, bufio.ErrBufferFull)
		for errors.Is(err, bufio.ErrBufferFull) {
			_, err = reader.ReadSlice('\n')
		}
		if err != nil && err != io.EOF {
			return err
		}
		var data []byte
		if !tooLong {
			data = bytes.TrimSuffix(bytes.TrimSuffix(line, []byte("\n")), []byte("\r"))
		}
		var reply []byte
		if tooLong || len(data) > requestLimit {
			reply = encode(nil, nil, -32600, "Request exceeds 64 KiB")
		} else if len(bytes.TrimSpace(data)) > 0 {
			reply = session.dispatch(context.Background(), data)
		}
		if len(reply) > 0 {
			if _, err := out.Write(append(reply, '\n')); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

type Limiter struct{ times []time.Time }

func (l *Limiter) AllowAfter(now time.Time, count int, gap time.Duration, n int) time.Duration {
	if n > count {
		return time.Minute
	}
	for len(l.times) > 0 && now.Sub(l.times[0]) >= time.Minute {
		l.times = l.times[1:]
	}
	var wait time.Duration
	if over := len(l.times) + n - count; over > 0 {
		wait = time.Minute - now.Sub(l.times[over-1])
	}
	if len(l.times) > 0 {
		wait = max(wait, gap-now.Sub(l.times[len(l.times)-1]))
	}
	if wait == 0 {
		for range n {
			l.times = append(l.times, now)
		}
	}
	return wait
}
