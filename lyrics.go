package main

import (
	"cmp"
	"context"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/betterlyrics"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/lyrics"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// lyricsState is the lyrics of the track playing, as far as they are loaded.
type lyricsState struct {
	load    task
	videoID string
	// total is the length the lyrics were looked up with, and 0 when the
	// track's own length was not known yet. Better Lyrics finds its cached
	// lyrics by it, so a lookup without one is made again once it is.
	total time.Duration
	// text is the lyrics as plain text, and synced the same with the moment
	// each line is sung and, when the source times them, each word.
	text   string
	synced []lyrics.Line
	footer string
	err    string
	// followed is the line the view last scrolled to, -1 before the first,
	// and centerPending says that line is still to be brought to the middle.
	// settled is whether the view has been placed on a line yet: the first
	// placement jumps, the rest glide.
	followed      int
	centerPending bool
	settled       bool
	// explain is whether the dialog about the source of the lyrics is open.
	explain bool
}

// loadedLyrics is what the lyrics of a track come to, from Better Lyrics,
// YouTube Music or LRCLIB.
type loadedLyrics struct {
	text, footer string
	synced       []lyrics.Line
}

// loadLyrics fetches the lyrics of the current track, once per track and once
// more once its length is known.
func (a *app) loadLyrics() {
	l := &a.nowPlaying.lyrics
	id := a.current.VideoID
	if id == "" || (l.videoID == id && (l.total > 0 || a.total <= 0)) {
		return
	}
	// A track looked up without its length keeps what it has while the
	// lookup is made again, so the lyrics do not blink back to loading.
	if again := l.videoID == id; !again {
		l.text, l.footer, l.err, l.synced, l.followed = "", "", "", nil, -1
		a.nowPlaying.lyricsScroll.Y = 0
		l.centerPending, l.settled = false, false
	}
	client, total := a.client(), a.total
	item := a.current
	if item.Artist == "" {
		// The list the track played from may have left its artist out while
		// another named it, and every lyrics source matches on it.
		item = a.withKnownCredits(item)
	}
	l.videoID, l.total = id, total
	sources := lyricSources{betterLyrics: a.betterLyrics, lrclib: a.lyricsSource}
	startTask(a, &l.load, panelTimeout, func(ctx context.Context) (loadedLyrics, error) {
		return findLyrics(ctx, sources, client, item, total)
	}, func(found loadedLyrics, err error) {
		l.text, l.footer, l.synced = found.text, found.footer, found.synced
		switch {
		case l.text != "" || len(l.synced) > 0:
		case err != nil:
			// Not that there are none: the lookup failed. It is not
			// remembered, so opening the tab or the next track tries again.
			l.err, l.videoID = "Couldn't load lyrics. Try again in a moment.", ""
		default:
			l.err = "No lyrics for this song."
		}
	})
}

// refreshLyrics looks the lyrics of the current track up again once the
// track's own length is known, which Better Lyrics keys its cache on. It only
// does so for a track whose lyrics were already asked for: a track nobody has
// shown lyrics for is left until they are.
func (a *app) refreshLyrics() {
	l := &a.nowPlaying.lyrics
	if l.videoID != a.current.VideoID || l.total > 0 || a.total <= 0 {
		return
	}
	a.loadLyrics()
}

// lyricSources are where a track's lyrics come from, in the order they are
// tried: Better Lyrics, which times each word, then YouTube Music, the
// service the track plays from, then LRCLIB.
type lyricSources struct {
	betterLyrics func(ctx context.Context, q betterlyrics.Query) ([]lyrics.Line, error)
	lrclib       lyricsSource
}

// findLyrics finds a track's lyrics, timed when it can, from the first source
// that has them. A source that fails is not an error: the next is tried, and
// no lyrics at all is an empty result.
func findLyrics(ctx context.Context, sources lyricSources, client *youtube.Client, item youtube.MusicItem, total time.Duration) (loadedLyrics, error) {
	var found, plain loadedLyrics
	var lookupErr error
	// A track that is not resolved yet has no length of its own; the one its
	// row lists is as good, and both services take a lookup without one.
	if total <= 0 {
		total = parseDuration(item.Duration)
	}
	if sources.betterLyrics != nil {
		lines, err := sources.betterLyrics(ctx, betterlyrics.Query{Title: item.Title, Artist: item.Artist, Album: item.Album, Duration: total})
		switch {
		case err != nil:
			log.Printf("lyrics: betterlyrics lookup failed: %v", err)
			lookupErr = err
		case len(lines) > 0:
			found.synced, found.footer = lines, "Source: Better Lyrics"
		}
	}
	if ctx.Err() != nil {
		return found, ctx.Err()
	}
	// Lines of words say what the plain text would, so YouTube Music is only
	// asked when Better Lyrics had none.
	if len(found.synced) == 0 {
		if tab, err := client.GetLyrics(ctx, item.VideoID); err == nil {
			plain = loadedLyrics{text: strings.TrimSpace(tab.Description), footer: tab.Footer}
			for _, line := range tab.Timed {
				found.synced = append(found.synced, lyrics.Line{At: line.Start, Text: line.Text})
			}
		} else if ctx.Err() != nil {
			return found, ctx.Err()
		}
		// Where the lyrics came from: what YouTube Music says of them, or
		// itself.
		found.footer = cmp.Or(plain.footer, "Source: YouTube Music")
	}
	if len(found.synced) == 0 && sources.lrclib != nil {
		got, err := sources.lrclib(ctx, lrclib.Query{Title: item.Title, Artist: item.Artist, Album: item.Album, Duration: total})
		if err != nil {
			log.Printf("lyrics: lrclib lookup failed: %v", err)
			lookupErr = cmp.Or(lookupErr, err)
		}
		if got != nil && len(got.Synced) > 0 {
			found.synced, found.footer = got.Synced, "Source: LRCLIB"
		}
		// Its plain text may hold a romanised copy as well; the timed lines
		// are cleaned of that, so they stand in for it when there are any.
		if plain.text == "" && got != nil && got.Plain != "" && len(found.synced) == 0 {
			plain = loadedLyrics{text: got.Plain, footer: "Source: LRCLIB"}
			found.footer = plain.footer
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

// lyricsSource finds the lyrics LRCLIB has of a track.
type lyricsSource func(ctx context.Context, q lrclib.Query) (*lrclib.Lyrics, error)

// lyricsView shows the lyrics of the track playing: timed, with the line
// being sung lit and followed, or as plain text. The user chooses between
// the two when the track has both.
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
		// The plain words are a page to read, so they keep a measure of their
		// own rather than the width of a panel that may be very wide.
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

// lyricsModes is the row above the lyrics: the switch between timed lines and
// plain words, and the button that says where the words came from.
func (a *app) lyricsModes(c *ui.Context, synced bool) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	ui.Row(c).Padding(0, 16, 0).Gap(18).AlignItems(ui.Center).Children(func() {
		// The clock stands for the timing the switch turns on, so that the row
		// needs no words of its own.
		ui.Icon(c, m3.IconSchedule).FontSize(18).TextColor(sc.OnSurfaceVariant)
		on := synced
		if m3.CompactSwitch(c, &on, "Synced lyrics").Tooltip("Synced lyrics").Changed() {
			a.settings.StaticLyrics = !on
			if !a.settings.StaticLyrics {
				// Back to the lines: the view finds the one being sung again
				// from the top, as it does for a track it has not shown yet.
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

// lyricsSourceDialog says which service the lyrics of the track playing came
// from, and the order Meiro asks them in.
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
		m3.Text(c, m3.BodyMedium, "Meiro shows the lyrics of the first source that has them: Better Lyrics first, then YouTube Music, then LRCLIB.").
			TextColor(sc.OnSurfaceVariant)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Close", Key: "close-lyrics-source"}).Clicked() {
				l.explain = false
			}
		})
	})
}

// lyricScroll is how a lyrics view glides to the line being sung.
var lyricScroll = ui.Smooth

// lyricMeasure is the width the timed lines are read at. A verse line fits on
// one or two lines of it, where a panel as wide as the window would leave the
// words trailing off to the edge.
const lyricMeasure = 440

// lyricFade is how far the timed lines dissolve into the card at the top and
// the bottom of the list, instead of being cut by its edge.
const lyricFade = 48

// syncedLyrics lists the lines, lights the one being sung and glides it to
// the middle as the song moves on. A line is dimmer and smaller the further it
// is from the one sung, and all of that eases rather than snaps. A line is a
// click away: it seeks.
func (a *app) syncedLyrics(c *ui.Context) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	scroll := &a.nowPlaying.lyricsScroll
	at := a.playbackPosition()
	now := lyrics.IndexAt(l.synced, at)
	if l.followed != now {
		l.followed, l.centerPending = now, true
	}
	viewport := ui.Rect{}
	ui.Column(c).Grow(1).MinHeight(0).Children(func() {
		view := ui.Scroll(c.Key("lyrics")).TrackScroll(scroll).Grow(1)
		viewport = view.LayoutBounds()
		// Room above the first line and below the last, so that each can
		// reach the middle.
		pad := max(viewport.H/2-40, 40)
		view.Padding(pad, 16, pad).Children(func() {
			ui.Column(c).FillWidth().MaxWidth(lyricMeasure).AlignSelf(ui.Center).Children(func() {
				for i, line := range l.synced {
					a.lyricLine(c, line, at, i, now, scroll, viewport, sc)
				}
			})
		})
		if now < 0 && l.centerPending && viewport.H > 0 {
			l.centerPending = false
			scroll.ScrollTo(0, 0, lyricScroll)
		}
		// The lines fade into the card where it ends, so the list reads as
		// continuing above and below rather than as text clipped by a box.
		surface := panelSurface(sc)
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(lyricFade).PassThrough().
			Gradient(surface, ui.Transparent, 180)
		ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(lyricFade).PassThrough().
			Gradient(ui.Transparent, surface, 180)
	})
}

// lyricLook is a line's opacity and size, as a fraction of the sung line's,
// by its distance from it: the sung line is full, its neighbours are dim and
// smaller, and the rest fade quickly into the background while staying
// legible enough to be found before they are sung.
func lyricLook(distance int) (opacity, size float32) {
	if distance == 0 {
		return 1, 1
	}
	k := float32(distance - 1)
	return max(0.4-0.12*k, 0.14), max(0.8-0.04*k, 0.6)
}

// unsungWord is how much of a word still to be sung shows, beside the ones
// already sung.
const unsungWord = 0.35

// lyricWords is a line in the spans of its words, each lit once it starts, so
// that a line fills in as it is sung.
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

// lyricLine is one line of the lyrics; now is the line being sung, at the
// position the song is at.
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
	// The line being sung takes the accent colour, so that the eye finds it
	// without having to read its weight or its brightness.
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
			// Both boxes are in window coordinates, so how far the middle of
			// the line is from the middle of the list is how far the list
			// still has to move.
			off := (bounds.Y + bounds.H/2) - (viewport.Y + viewport.H/2)
			switch {
			case !l.settled:
				// The card is still opening and its middle moves with it:
				// place the line sharply, and again on the next frame, until
				// it is where the middle is. Every move after that glides.
				if max(off, -off) > 0.5 {
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
