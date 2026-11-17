package main

import "github.com/elianiva/meiro/internal/settings"

func (a *app) zoom() float32 {
	return float32(settings.ClampZoom(a.settings.Zoom)) / 100
}

func (a *app) zoomTo(percent int) {
	percent = settings.ClampZoom(percent)
	if percent == settings.ClampZoom(a.settings.Zoom) {
		return
	}
	a.settings.Zoom = percent
	a.saveSettings()
}

func (a *app) zoomBy(steps int) {
	a.zoomTo(settings.ClampZoom(a.settings.Zoom) + steps*settings.ZoomStep)
}

func (a *app) resetZoom() { a.zoomTo(settings.DefaultZoom) }
