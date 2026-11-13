// Package sentry turns a crash into a Sentry event with the official
// sentry-go SDK. It sends only what the user agreed to: the details the crash
// dialog shows, and nothing that could name a person.
//
// Secrets and web addresses are removed here, at the boundary, so a caller
// cannot send them by accident.
package sentry

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	sentrygo "github.com/getsentry/sentry-go"

	"github.com/elianiva/meiro/internal/applog"
)

// Timeout bounds one report, so a slow network never holds up what sends it.
const Timeout = 10 * time.Second

// Client sends crash reports to one project.
type Client struct {
	sdk *sentrygo.Client
}

// New makes a client for the project the DSN names. release is the app's
// version, empty for a development build.
func New(dsn, release string) (*Client, error) {
	tag := ""
	environment := "development"
	if release != "" {
		tag, environment = "meiro@"+release, "production"
	}
	sdk, err := sentrygo.NewClient(sentrygo.ClientOptions{
		Dsn:              dsn,
		Release:          tag,
		Environment:      environment,
		ServerName:       "meiro",
		AttachStacktrace: false,
		EnableTracing:    false,
	})
	if err != nil {
		return nil, fmt.Errorf("sentry: %w", err)
	}
	return &Client{sdk: sdk}, nil
}

// Crash is one panic to report.
type Crash struct {
	// Where is what the app was doing, and Message what it panicked with.
	Where, Message string
	// Details is the report the crash dialog offers to copy, with the
	// versions, the panic and its stack.
	Details string
	// Stack is the goroutine dump debug.Stack returned, parsed into the
	// frames Sentry groups by.
	Stack string
}

// Capture sends one crash report. It waits for the SDK to hand the event to
// its transport, so a failure can be logged while the app is still up.
func (c *Client) Capture(crash Crash) error {
	if c == nil || c.sdk == nil {
		return errors.New("sentry: no client")
	}
	message := applog.Redact(Redact(crash.Message))
	handled := false
	event := sentrygo.NewEvent()
	event.Platform = "go"
	event.Timestamp = time.Now()
	event.Level = sentrygo.LevelFatal
	event.Message = "Panic while " + crash.Where + ": " + message
	event.Tags = map[string]string{"where": crash.Where}
	event.Contexts["crash"] = map[string]any{"details": applog.Redact(Redact(crash.Details))}
	event.Exception = []sentrygo.Exception{{
		Type:      "panic",
		Value:     message,
		Mechanism: &sentrygo.Mechanism{Type: "panic", Handled: &handled},
	}}
	if frames := parseFrames(crash.Stack); len(frames) > 0 {
		event.Exception[0].Stacktrace = &sentrygo.Stacktrace{Frames: frames}
	}
	c.sdk.CaptureEvent(event, nil, nil)
	if !c.sdk.Flush(Timeout) {
		return errors.New("sentry: the report did not leave in time")
	}
	return nil
}

// parseFrames turns the goroutine dump debug.Stack returns into Sentry
// frames, oldest first. It reads the goroutine that panicked, which is the
// first block, and stops at the blank line before the others.
func parseFrames(stack string) []sentrygo.Frame {
	var frames []sentrygo.Frame
	lines := strings.Split(stack, "\n")
	for i := 0; i < len(lines); i++ {
		function := strings.TrimSpace(lines[i])
		if function == "" && len(frames) > 0 {
			break
		}
		if function == "" || strings.HasPrefix(lines[i], "\t") ||
			strings.HasPrefix(function, "goroutine ") || strings.HasPrefix(function, "...") {
			continue
		}
		if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "\t") {
			continue
		}
		file, line := location(strings.TrimSpace(lines[i+1]))
		if file == "" || line == 0 {
			continue
		}
		frames = append(frames, sentrygo.Frame{
			Filename: file,
			Function: functionName(function),
			Lineno:   line,
			InApp:    strings.Contains(function, "github.com/elianiva/meiro") || strings.HasPrefix(function, "main."),
		})
		i++
	}
	// Sentry shows the newest frame last.
	for left, right := 0, len(frames)-1; left < right; left, right = left+1, right-1 {
		frames[left], frames[right] = frames[right], frames[left]
	}
	return frames
}

// location reads the file and line from a stack frame's "path:line +0x1d".
func location(line string) (string, int) {
	if i := strings.Index(line, " +0x"); i >= 0 {
		line = line[:i]
	}
	colon := strings.LastIndex(line, ":")
	if colon <= 0 {
		return "", 0
	}
	number, err := strconv.Atoi(line[colon+1:])
	if err != nil || number <= 0 {
		return "", 0
	}
	return line[:colon], number
}

// functionName drops the argument list Go prints after a function.
func functionName(line string) string {
	if strings.HasSuffix(line, ")") {
		if i := strings.LastIndex(line, "("); i > 0 {
			return line[:i]
		}
	}
	return line
}

// addressPattern matches a web address, which can name a video or carry a
// token.
var addressPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

// Redact replaces web addresses with a placeholder.
func Redact(s string) string {
	return addressPattern.ReplaceAllString(s, "<url>")
}
