package report

import (
	"bytes"
	"context"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/proc"
)

const issuesURL = "https://github.com/elianiva/meiro/issues/new"

const versionTimeout = 5 * time.Second

const Unknown = "unknown"

type Info struct {
	App    string
	OS     string
	Arch   string
	YtDlp  string
	FFmpeg string
}

func Collect(ctx context.Context, app string) Info {
	return Info{
		App:    app,
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		YtDlp:  toolVersion(ctx, "yt-dlp", "--version", ParseYtDlp),
		FFmpeg: FFmpeg(ctx),
	}
}

func FFmpeg(ctx context.Context) string {
	return toolVersion(ctx, "ffmpeg", "-version", ParseFFmpeg)
}

func toolVersion(ctx context.Context, name, flag string, parse func(string) string) string {
	path, err := proc.Tool(name)
	if err != nil {
		return "not found"
	}
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()
	cmd := proc.Command(ctx, path, flag)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return Unknown
	}
	if v := parse(out.String()); v != "" {
		return v
	}
	return Unknown
}

func ParseYtDlp(output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(line)
}

func ParseFFmpeg(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(line)
	if len(fields) >= 3 && fields[0] == "ffmpeg" && fields[1] == "version" {
		return fields[2]
	}
	return ""
}

func (i Info) Version() string {
	if i.App == "" {
		return "development build"
	}
	return i.App
}

func (i Info) Tools() string {
	return "yt-dlp: " + orUnknown(i.YtDlp) + "\nffmpeg: " + orUnknown(i.FFmpeg)
}

func (i Info) URL() string {
	q := url.Values{}
	q.Set("template", "bug_report.yml")
	q.Set("version", i.Version())
	q.Set("os-version", i.OS+" "+i.Arch)
	q.Set("tools", i.Tools())
	return issuesURL + "?" + q.Encode()
}

func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}
