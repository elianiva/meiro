package report

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseFFmpeg(t *testing.T) {
	for in, want := range map[string]string{
		"ffmpeg version 7.1 Copyright (c) 2000-2024 the FFmpeg developers\nbuilt with gcc": "7.1",
		"ffmpeg version n6.0-12-gabc Copyright":                                            "n6.0-12-gabc",
		"something else entirely":                                                          "",
		"":                                                                                 "",
	} {
		if got := ParseFFmpeg(in); got != want {
			t.Errorf("ParseFFmpeg(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseYtDlp(t *testing.T) {
	if got := ParseYtDlp("2025.01.15\n"); got != "2025.01.15" {
		t.Errorf("got %q", got)
	}
	if got := ParseYtDlp("  \n"); got != "" {
		t.Errorf("blank output gave %q", got)
	}
}

// The link round-trips the details, including characters that need escaping,
// and says what is missing instead of leaving a blank.
func TestURLCarriesTheDetails(t *testing.T) {
	info := Info{App: "1.2.3", OS: "linux", Arch: "arm64", YtDlp: "2025.01.15", FFmpeg: ""}
	raw := info.URL()
	if !strings.HasPrefix(raw, "https://github.com/elianiva/meiro/issues/new?") {
		t.Fatalf("unexpected link %s", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	body := u.Query().Get("body")
	for _, want := range []string{"- Meiro: 1.2.3\n", "- OS: linux arm64\n", "- yt-dlp: 2025.01.15\n", "- ffmpeg: unknown\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}

func TestBodyNamesDevelopmentBuilds(t *testing.T) {
	if body := (Info{}).Body(); !strings.Contains(body, "- Meiro: development build\n") {
		t.Errorf("body:\n%s", body)
	}
}
