package main

import (
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
)

type themeState struct {
	shown                   *m3.Theme
	to                      *m3.Theme
	from                    m3.Scheme
	at                      time.Time
	cached                  *m3.Theme
	cachedCfg               m3.Config
	cachedDark              bool
	dynamic                 ui.Color
	dynamicOK               bool
	dynamicFor              string
	previews                map[m3.Style]m3.Scheme
	previewSeed             ui.Color
	previewDark             bool
	buttonsDark, buttonsSet bool
}

const themeGlide = 420 * time.Millisecond

func (a *app) resolveTheme(c *ui.Context) *m3.Theme {
	t := &a.theme
	cfg := a.settings.Theme(uiFont)
	if a.settings.Dynamic && !a.settings.Matugen && t.dynamicOK {
		cfg.Seed = t.dynamic
	}
	if a.settings.Matugen {
		cfg.Colors = a.colors
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
			t.at = time.Time{}
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

func (a *app) followArtwork() {
	t := &a.theme
	art := a.current.Thumbnail
	if !a.settings.Dynamic || a.settings.Matugen || art == "" || t.dynamicFor == art {
		return
	}
	colour, has, ready := a.thumbs.Colour(art)
	if !ready {
		return
	}
	t.dynamicFor = art
	if has {
		t.dynamic, t.dynamicOK = colour, true
	}
}
