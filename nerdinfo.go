package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/ytdlp"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
)

// resolveTrace is how one track's audio was found, kept for the playback
// details.
type resolveTrace struct {
	VideoID string
	// Source is who found the audio: the player request or yt-dlp.
	Source string
	// Signed says the account's cookies went with the request.
	Signed bool
	URL    string
	// DirectError is why the player request failed, when yt-dlp took over.
	DirectError string
	Elapsed     time.Duration
	Run         ytdlp.Run
}

const traceLimit = 16

// resolveTraces holds the latest traces. Resolving runs off the main thread.
type resolveTraces struct {
	mu    sync.Mutex
	items []resolveTrace
}

func (t *resolveTraces) put(trace resolveTrace) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, old := range t.items {
		if old.VideoID == trace.VideoID {
			t.items = append(t.items[:i], t.items[i+1:]...)
			break
		}
	}
	t.items = append(t.items, trace)
	if len(t.items) > traceLimit {
		t.items = t.items[1:]
	}
}

func (t *resolveTraces) get(videoID string) (resolveTrace, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, trace := range t.items {
		if trace.VideoID == videoID {
			return trace, true
		}
	}
	return resolveTrace{}, false
}

// playingAudio is where the track playing came from.
type playingAudio struct {
	videoID string
	// url is the stream's address, or the path of the file when file is set.
	url  string
	file bool
}

type nerdState struct{ open bool }

// audioFormats are the audio formats YouTube serves, by itag.
var audioFormats = map[string]string{
	"139": "AAC, 48 kbps", "140": "AAC, 128 kbps", "141": "AAC, 256 kbps (Premium)",
	"249": "Opus, ~50 kbps", "250": "Opus, ~70 kbps", "251": "Opus, ~130 kbps", "774": "Opus, ~256 kbps (Premium)",
}

// detailRow is one labelled line of the playback details.
type detailRow struct{ label, value string }

// streamAddress is the address of the stream playing, or "" when the track
// plays from a file.
func (a *app) streamAddress() string {
	if a.playing.videoID == a.current.VideoID {
		if a.playing.file {
			return ""
		}
		return a.playing.url
	}
	trace, _ := a.traces.get(a.current.VideoID)
	return trace.URL
}

// playbackDetails lists what is known of how the current track plays.
func (a *app) playbackDetails() []detailRow {
	var rows []detailRow
	add := func(label, value string) {
		if value != "" {
			rows = append(rows, detailRow{label, value})
		}
	}
	add("Track", a.current.Title+" ("+a.current.VideoID+")")
	trace, traced := a.traces.get(a.current.VideoID)
	stream := a.streamAddress()
	if a.playing.videoID == a.current.VideoID && a.playing.file {
		add("Source", "File on disk")
		add("File", a.playing.url)
	} else if traced {
		add("Source", trace.Source)
		add("Resolved in", trace.Elapsed.Round(time.Millisecond).String())
		if trace.Signed {
			add("Account", "Signed in, cookies sent to yt-dlp")
		} else {
			add("Account", "Anonymous")
		}
		add("Player request failed", trace.DirectError)
		if trace.Run.Args != nil {
			add("yt-dlp", strings.Join(trace.Run.Args, " "))
			add("JS runtime", trace.Run.JSRuntime)
		}
	}
	for _, row := range streamRows(stream) {
		add(row.label, row.value)
	}
	add("Audio from account", map[bool]string{true: "On", false: "Off"}[a.settings.AccountAudio])
	add("Output", player.OutputFormat())
	add("Speed", strconv.FormatFloat(a.player.Speed(), 'f', -1, 64)+"×")
	add("Volume", strconv.Itoa(int(a.player.Volume()*100+0.5))+"%")
	if a.loudnessOK {
		add("Loudness", fmt.Sprintf("%.1f dB", a.loudness))
	}
	add("Equaliser", map[bool]string{true: "On", false: "Off"}[a.settings.EQOn])
	return rows
}

// streamRows reads what YouTube puts in a media URL: the format, the size, and
// from them the bitrate.
func streamRows(stream string) []detailRow {
	parsed, err := url.Parse(stream)
	if err != nil || parsed.Host == "" {
		return nil
	}
	q := parsed.Query()
	var rows []detailRow
	rows = append(rows, detailRow{"Host", parsed.Host})
	if itag := q.Get("itag"); itag != "" {
		format := audioFormats[itag]
		if format == "" {
			format = "unknown"
		}
		rows = append(rows, detailRow{"Format", "itag " + itag + ": " + format})
	}
	if mime := q.Get("mime"); mime != "" {
		rows = append(rows, detailRow{"Container", mime})
	}
	size, _ := strconv.ParseFloat(q.Get("clen"), 64)
	seconds, _ := strconv.ParseFloat(q.Get("dur"), 64)
	if size > 0 {
		rows = append(rows, detailRow{"Size", fmt.Sprintf("%.2f MB", size/1e6)})
	}
	if size > 0 && seconds > 0 {
		rows = append(rows, detailRow{"Average bitrate", fmt.Sprintf("%.0f kbps", size*8/seconds/1000)})
	}
	return rows
}

// nerdDialog shows the details of how the current track plays, with a button
// that copies them. The stream address has a button of its own, and stays out
// of the copy: it carries a signature that works for anyone who has it.
func (a *app) nerdDialog(c *ui.Context) {
	d := a.nerd
	if d == nil {
		return
	}
	if !d.open {
		a.nerd = nil
		return
	}
	sc := m3.Of(c).Scheme
	rows := a.playbackDetails()
	m3.Dialog(c, &d.open, 560, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Playback details")
		ui.Column(c).Padding(12, 16).Gap(8).Radius(m3.Large).Background(sc.SurfaceContainerHighest).Children(func() {
			for i, row := range rows {
				ui.Column(c).Gap(0).Key(c.Key("nerd-" + strconv.Itoa(i))).Children(func() {
					m3.Text(c, m3.LabelSmall, row.label).TextColor(sc.OnSurfaceVariant)
					m3.Text(c, m3.BodyMedium, row.value).TextColor(sc.OnSurface)
				})
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if stream := a.streamAddress(); stream != "" && m3.Button(c, m3.ButtonSpec{Label: "Copy URL", Kind: m3.TextOnly, Key: "copy-nerd-url"}).Clicked() {
				c.WriteClipboard(stream)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Copy", Kind: m3.TextOnly, Key: "copy-nerd"}).Clicked() {
				var out strings.Builder
				for _, row := range rows {
					fmt.Fprintf(&out, "%s: %s\n", row.label, row.value)
				}
				c.WriteClipboard(out.String())
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Close", Key: "close-nerd"}).Clicked() {
				a.nerd = nil
			}
		})
	})
}
