package main

import (
	"cmp"
	"context"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/lyrics"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

type lyricsState struct {
	load          task
	videoID       string
	total         time.Duration
	text          string
	synced        []lyrics.Line
	footer        string
	err           string
	followed      int
	centerPending bool
	settled       bool
	explain       bool
}

type loadedLyrics struct {
	text, footer string
	synced       []lyrics.Line
}

func (a *app) loadLyrics() {
	l := &a.nowPlaying.lyrics
	id := a.current.VideoID
	sameTrack := l.videoID == id
	lookupIsFinal := l.total > 0 || a.total <= 0
	if id == "" || (sameTrack && lookupIsFinal) {
		return
	}
	if again := l.videoID == id; !again {
		l.text, l.footer, l.err, l.synced, l.followed = "", "", "", nil, -1
		a.nowPlaying.lyricsScroll.Y = 0
		l.centerPending, l.settled = false, false
	}
	client, total := a.client(), a.total
	item := a.current
	if item.Artist == "" {
		item = a.withKnownCredits(item)
	}
	l.videoID, l.total = id, total
	startTask(a, &l.load, panelTimeout, func(ctx context.Context) (loadedLyrics, error) {
		return findLyrics(ctx, a.lyricsSource, client, item, total)
	}, func(found loadedLyrics, err error) {
		l.text, l.footer, l.synced = found.text, found.footer, found.synced
		switch {
		case l.text != "" || len(l.synced) > 0:
		case err != nil:
			l.err, l.videoID = "Couldn't load lyrics. Try again in a moment.", ""
		default:
			l.err = "No lyrics for this song."
		}
	})
}

func (a *app) refreshLyrics() {
	l := &a.nowPlaying.lyrics
	lookedUpWithoutLength := l.videoID == a.current.VideoID && l.total <= 0
	lengthNowKnown := a.total > 0
	if !lookedUpWithoutLength || !lengthNowKnown {
		return
	}
	a.loadLyrics()
}

// prefetchNextLyrics warms the engine's cache for the track that will play
// next, so its lyrics open from the cache instead of the network.
func (a *app) prefetchNextLyrics() {
	if a.lyricsSource == nil {
		return
	}
	index, ok := a.queue.next(a.settings.AutoPlay, a.repeat)
	if !ok {
		return
	}
	item := a.queue.at(index)
	if item.VideoID == "" || item.VideoID == a.current.VideoID || item.IsEpisode() {
		return
	}
	a.prefetchLyrics(item)
}

func (a *app) prefetchLyrics(item youtube.MusicItem) {
	if a.lyricsSource == nil || item.VideoID == "" || item.IsEpisode() {
		return
	}
	if item.Artist == "" {
		item = a.withKnownCredits(item)
	}
	startTask(a, &a.lyricsPrefetch, panelTimeout, func(ctx context.Context) (lyrics.Result, error) {
		return a.lyricsSource(ctx, lyrics.Query{Title: item.Title, Artist: item.Artist, Album: item.Album, Duration: parseDuration(item.Duration)})
	}, func(lyrics.Result, error) {})
}

func findLyrics(ctx context.Context, lookup lyrics.Source, client *youtube.Client, item youtube.MusicItem, total time.Duration) (loadedLyrics, error) {
	var found, plain loadedLyrics
	var lookupErr error
	if total <= 0 {
		total = parseDuration(item.Duration)
	}
	if lookup != nil {
		got, err := lookup(ctx, lyrics.Query{Title: item.Title, Artist: item.Artist, Album: item.Album, Duration: total})
		source := "Source: " + cmp.Or(got.Source, "Better Lyrics")
		switch {
		case err != nil:
			log.Printf("lyrics: lookup failed: %v", err)
			lookupErr = err
		case len(got.Synced) > 0:
			found.synced, found.footer = got.Synced, source
			if got.Plain != "" {
				plain = loadedLyrics{text: strings.TrimSpace(got.Plain), footer: source}
			}
		case got.Plain != "":
			plain = loadedLyrics{text: strings.TrimSpace(got.Plain), footer: source}
		}
	}
	if ctx.Err() != nil {
		return found, ctx.Err()
	}
	if len(found.synced) == 0 {
		footer := "Source: YouTube Music"
		tab, err := client.GetLyrics(ctx, item.VideoID)
		switch {
		case err == nil:
			footer = cmp.Or(tab.Footer, footer)
			text := strings.TrimSpace(tab.Description)
			var synced []lyrics.Line
			for _, line := range tab.Timed {
				synced = append(synced, lyrics.Line{At: line.Start, Text: line.Text})
			}
			switch {
			case len(synced) > 0:
				found.synced, found.footer = synced, footer
				plain = loadedLyrics{text: text, footer: footer}
			case text != "" && plain.text == "":
				plain = loadedLyrics{text: text, footer: footer}
				found.footer = footer
			case plain.text == "":
				found.footer = footer
			}
		case ctx.Err() != nil:
			return found, ctx.Err()
		case plain.text == "":
			found.footer = footer
		}
	}
	found.text = plain.text
	defer func() {
		log.Printf("lyrics: video_id=%s title=%q artist=%q album=%q total=%s -> %s, synced_lines=%d plain_chars=%d",
			item.VideoID, item.Title, item.Artist, item.Album, total, found.footer, len(found.synced), len(found.text))
	}()
	if found.text == "" && len(found.synced) > 0 {
		found.text = lyrics.Text(found.synced)
	}
	if found.text == "" && len(found.synced) == 0 {
		return found, lookupErr
	}
	return found, nil
}

func (a *app) lyricsView(c *ui.Context) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	switch {
	case l.load.busy():
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicator(c, 56, true) })
	case l.err != "":
		a.message(c, m3.IconLyrics, l.err, "", "", nil)
	default:
		timed := len(l.synced) > 0
		synced := timed && !a.settings.StaticLyrics
		if timed {
			a.lyricsModes(c, synced)
		}
		if synced {
			a.syncedLyrics(c)
			return
		}
		ui.Scroll(c).Grow(1).Padding(16, 24, 40).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(lyricMeasure).AlignSelf(ui.Center).Children(func() {
				m3.Text(c, m3.BodyLarge, l.text).LineHeight(1.7).TextColor(sc.OnSurface).Selectable()
				if l.footer != "" && !timed {
					m3.Text(c, m3.BodySmall, l.footer).TextColor(sc.OnSurfaceVariant).Margin(16, 0, 0)
				}
			})
		})
	}
}

func (a *app) lyricsModes(c *ui.Context, synced bool) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	ui.Row(c).Padding(0, 16, 0).Gap(18).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, m3.IconSchedule).FontSize(18).TextColor(sc.OnSurfaceVariant)
		on := synced
		if m3.CompactSwitch(c, &on, "Synced lyrics").Tooltip("Synced lyrics").Changed() {
			a.settings.StaticLyrics = !on
			if !a.settings.StaticLyrics {
				l.followed, l.settled = -1, false
			}
			a.saveSettings()
		}
		ui.Box(c).Grow(1)
		if l.footer != "" {
			if m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconHelp, Label: "Where these lyrics come from", Key: "lyrics-source",
			}).Clicked() {
				l.explain = true
			}
		}
	})
}

func (a *app) lyricsSourceDialog(c *ui.Context) {
	l := &a.nowPlaying.lyrics
	if !l.explain {
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &l.explain, 460, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Where these lyrics come from")
		if source := strings.TrimPrefix(l.footer, "Source: "); source != "" {
			m3.Text(c, m3.BodyLarge, source).TextColor(sc.OnSurface)
		}
		m3.Text(c, m3.BodyMedium, "Meiro prefers word-synced lyrics from Better Lyrics, Unison, LyricsPlus, KuGou and LRCLIB, then falls back to YouTube Music.").
			TextColor(sc.OnSurfaceVariant)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Close", Key: "close-lyrics-source"}).Clicked() {
				l.explain = false
			}
		})
	})
}

var lyricScroll = ui.Smooth

const lyricMeasure = 440

const lyricFade = 48

func (a *app) syncedLyrics(c *ui.Context) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	scroll := &a.nowPlaying.lyricsScroll
	at := a.playbackPosition()
	now := lyrics.IndexAt(l.synced, at)
	if l.followed != now {
		l.followed, l.centerPending = now, true
	}
	if now <= 0 && l.centerPending {
		l.centerPending, l.settled = false, false
		scroll.Y = 0
	}
	viewport := ui.Rect{}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		view := ui.Scroll(c.Key("lyrics")).TrackScroll(scroll).Grow(1)
		viewport = view.LayoutBounds()
		pad := max(viewport.H/2-40, 40)
		if now <= 0 {
			pad = 16
		}
		view.Padding(pad, 16, pad).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(lyricMeasure).AlignSelf(ui.Center).Children(func() {
				for i, line := range l.synced {
					a.lyricLine(c, line, at, i, now, scroll, viewport, sc)
				}
			})
		})
		surface := panelSurface(sc)
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(lyricFade).PassThrough().
			Gradient(surface, ui.Transparent, 180)
		ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(lyricFade).PassThrough().
			Gradient(ui.Transparent, surface, 180)
	})
}

const centeredWithinDIPs = 0.5

const (
	nearUnsungOpacity = 0.4
	opacityFalloff    = 0.12
	farUnsungOpacity  = 0.14
	nearUnsungSize    = 0.8
	sizeFalloff       = 0.04
	farUnsungSize     = 0.6
)

func lyricLook(distance int) (opacity, size float32) {
	if distance == 0 {
		return 1, 1
	}
	k := float32(distance - 1)
	return max(nearUnsungOpacity-opacityFalloff*k, farUnsungOpacity), max(nearUnsungSize-sizeFalloff*k, farUnsungSize)
}

const unsungWord = 0.35

func lyricWords(line lyrics.Line, at time.Duration, lit ui.Color) []ui.Span {
	sung := lyrics.IndexWordAt(line.Words, at)
	spans := make([]ui.Span, len(line.Words))
	for i, word := range line.Words {
		color := lit.Alpha(unsungWord)
		if i <= sung {
			color = lit
		}
		spans[i] = ui.Span{Text: word.Text, Color: color}
	}
	return spans
}

func (a *app) lyricLine(c *ui.Context, line lyrics.Line, at time.Duration, i, now int, scroll *ui.ScrollState, viewport ui.Rect, sc m3.Scheme) {
	l := &a.nowPlaying.lyrics
	distance := i - now
	if distance < 0 {
		distance = -distance
	}
	opacity, size := lyricLook(distance)
	text := line.Text
	if text == "" {
		text = "♪"
	}
	lit := sc.OnSurface
	if i == now {
		lit = sc.Primary
	}
	row := ui.ButtonBase(c.Key(i)).FillWidth().Padding(8, 14).Radius(m3.Large).Justify(ui.Start).
		Cursor(ui.CursorPointer).Label(text)
	row.Background(m3.StateFill(ui.Transparent, lit, row.Hovered(), row.Pressed(), row.FocusVisible()))
	opacity = m3.Animate(row, "opacity", opacity, m3.EffectsDefault)
	size = m3.Animate(row, "size", size, m3.EffectsSlow)
	row.Opacity(opacity).TransformOrigin(0, 0.5).Scale(size, size)
	row.Children(func() {
		if len(line.Words) > 0 {
			m3.EmphasizedRichText(c, m3.HeadlineSmall, lyricWords(line, at, lit)...).FontWeight(800).LineHeight(1.25)
			return
		}
		m3.EmphasizedText(c, m3.HeadlineSmall, text).FontWeight(800).TextColor(lit).LineHeight(1.25)
	})
	if row.Clicked() {
		a.seek(line.At)
	}
	if i == now && l.centerPending {
		bounds := row.LayoutBounds()
		if bounds.H > 0 && viewport.H > 0 {
			off := (bounds.Y + bounds.H/2) - (viewport.Y + viewport.H/2)
			switch {
			case !l.settled:
				if max(off, -off) > centeredWithinDIPs {
					scroll.Y += off
				} else {
					l.settled, l.centerPending = true, false
				}
			default:
				l.centerPending = false
				scroll.ScrollTo(0, scroll.Y+off, lyricScroll)
			}
		}
	}
}
