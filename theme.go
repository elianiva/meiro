package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
)

// themeState is the theme being shown. A change of colours glides from one
// scheme to the next rather than cutting.
type themeState struct {
	shown *m3.Theme
	to    *m3.Theme
	from  m3.Scheme
	at    time.Time
	// cached is the last theme resolved, reused while the configuration and
	// appearance that make it are unchanged.
	cached     *m3.Theme
	cachedCfg  m3.Config
	cachedDark bool
	// dynamic is the seed taken from the artwork playing, and dynamicFor the
	// artwork it was taken from.
	dynamic    ui.Color
	dynamicOK  bool
	dynamicFor string
	// previews caches the palette-style preview schemes the settings show,
	// until the seed or appearance they are drawn from changes.
	previews    map[m3.Style]m3.Scheme
	previewSeed ui.Color
	previewDark bool
	// buttonsDark is the appearance the window buttons were last drawn for,
	// once buttonsSet.
	buttonsDark, buttonsSet bool
}

// themeGlide is how long a change of colours takes.
const themeGlide = 420 * time.Millisecond

// resolveTheme returns the theme to draw this frame with: the one the
// settings describe, glided to from the one before when it changed.
func (a *app) resolveTheme(c *ui.Context) *m3.Theme {
	t := &a.theme
	cfg := a.settings.Theme(uiFont)
	if a.settings.Dynamic && t.dynamicOK {
		cfg.Seed = t.dynamic
	}
	dark := c.Theme().Dark
	if t.cached == nil || t.cachedCfg != cfg || t.cachedDark != dark {
		t.cached, t.cachedCfg, t.cachedDark = m3.New(cfg, dark), cfg, dark
	}
	target := t.cached
	if t.to == nil {
		t.to, t.shown, t.from = target, target, target.Scheme
		return target
	}
	if target.Config != t.to.Config || target.Dark != t.to.Dark {
		t.from = t.shown.Scheme
		t.to, t.at = target, c.Now()
		if c.Preferences().ReduceMotion {
			t.at = time.Time{} // no glide: cut to the new colours
		}
	}
	k := float32(c.Now().Sub(t.at)) / float32(themeGlide)
	if k >= 1 || t.at.IsZero() {
		t.shown = t.to
		return t.shown
	}
	shown := *t.to
	shown.Scheme = t.from.Mix(t.to.Scheme, 1-(1-k)*(1-k)*(1-k))
	t.shown = &shown
	c.AnimationFrame()
	return t.shown
}

// followArtwork takes the theme's seed from the artwork of the track playing,
// when the user asked for it.
func (a *app) followArtwork() {
	t := &a.theme
	art := a.current.Thumbnail
	if !a.settings.Dynamic || art == "" || t.dynamicFor == art {
		return
	}
	colour, has, ready := a.thumbs.Colour(art, playerArt)
	if !ready {
		return // the picture or its colour is still on the way; the cache wakes the view
	}
	t.dynamicFor = art
	if has {
		t.dynamic, t.dynamicOK = colour, true
	}
}
