// Package report builds the link that opens a new GitHub issue with the
// details a bug report needs: the app version, the system, and the versions
// of the helper programs. It holds nothing of the user's: no account, no
// cookie, no path, no history.
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

// issuesURL is where a new issue is opened.
const issuesURL = "https://github.com/elianiva/meiro/issues/new"

// versionTimeout bounds one version lookup, so a broken tool cannot hold the
// report back.
const versionTimeout = 5 * time.Second

// Unknown stands in for a version that could not be read.
const Unknown = "unknown"

// Info is everything a report carries.
type Info struct {
	App    string // the Meiro version, empty for a development build
	OS     string // runtime.GOOS
	Arch   string // runtime.GOARCH
	YtDlp  string
	FFmpeg string
}

// Collect reads the versions of the helper programs, which runs them: call
// it off the main thread. app is the Meiro version.
func Collect(ctx context.Context, app string) Info {
	return Info{
		App:    app,
		OS:     runtime.GOOS,
		Arch:   runtime.GOARCH,
		YtDlp:  toolVersion(ctx, "yt-dlp", "--version", ParseYtDlp),
		FFmpeg: toolVersion(ctx, "ffmpeg", "-version", ParseFFmpeg),
	}
}

// toolVersion runs the tool the app would run and parses its answer.
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

// ParseYtDlp reads the version from `yt-dlp --version`, which prints just
// that, such as "2025.01.15".
func ParseYtDlp(output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(line)
}

// ParseFFmpeg reads the version from the first line of `ffmpeg -version`,
// such as "ffmpeg version 7.1 Copyright (c) 2000-2024 the FFmpeg developers".
func ParseFFmpeg(output string) string {
	line, _, _ := strings.Cut(output, "\n")
	fields := strings.Fields(line)
	if len(fields) >= 3 && fields[0] == "ffmpeg" && fields[1] == "version" {
		return fields[2]
	}
	return ""
}

// Body is the text the issue starts with.
func (i Info) Body() string {
	app := i.App
	if app == "" {
		app = "development build"
	}
	var b strings.Builder
	b.WriteString("**What happened?**\n\n\n")
	b.WriteString("**What did you expect?**\n\n\n")
	b.WriteString("**Steps to reproduce**\n\n1. \n\n")
	b.WriteString("---\n")
	b.WriteString("- Meiro: " + app + "\n")
	b.WriteString("- OS: " + i.OS + " " + i.Arch + "\n")
	b.WriteString("- yt-dlp: " + orUnknown(i.YtDlp) + "\n")
	b.WriteString("- ffmpeg: " + orUnknown(i.FFmpeg) + "\n")
	return b.String()
}

// URL is the link that opens a new issue with the details filled in.
func (i Info) URL() string {
	q := url.Values{}
	q.Set("body", i.Body())
	return issuesURL + "?" + q.Encode()
}

func orUnknown(s string) string {
	if s == "" {
		return Unknown
	}
	return s
}
