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

const Timeout = 10 * time.Second

type Client struct {
	sdk *sentrygo.Client
}

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

type Crash struct {
	Where, Message string
	Details        string
	Stack          string
}

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
	return newestLast(frames)
}

func newestLast(frames []sentrygo.Frame) []sentrygo.Frame {
	for left, right := 0, len(frames)-1; left < right; left, right = left+1, right-1 {
		frames[left], frames[right] = frames[right], frames[left]
	}
	return frames
}

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

func functionName(line string) string {
	if strings.HasSuffix(line, ")") {
		if i := strings.LastIndex(line, "("); i > 0 {
			return line[:i]
		}
	}
	return line
}

var addressPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func Redact(s string) string {
	return addressPattern.ReplaceAllString(s, "<url>")
}
