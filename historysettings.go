package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/m3"
)

func (a *app) historyCard(c *ui.Context) {
	a.settingsCard(c, "Listening history", m3.IconHistory, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			body := fmt.Sprintf("%d plays are kept on this device, for History and the stats. Meiro keeps the latest %d.", len(a.history), history.MaxEntries)
			if len(a.history) == 0 {
				body = "No plays are kept on this device."
			}
			settingLabel(c, "Plays", body).Grow(1)
			switch {
			case len(a.history) == 0:
				a.confirmClearHistory = false
				m3.Button(c, m3.ButtonSpec{Label: "Clear history", Kind: m3.Tonal, Size: m3.Medium56, Disabled: true, Key: "clear-history"})
			case !a.confirmClearHistory:
				if m3.Button(c, m3.ButtonSpec{Label: "Clear history", Kind: m3.Tonal, Size: m3.Medium56, Key: "clear-history"}).Clicked() {
					a.confirmClearHistory = true
				}
			default:
				ui.Row(c).Gap(8).Children(func() {
					if m3.Button(c, m3.ButtonSpec{Label: "Keep", Kind: m3.Outlined, Size: m3.Medium56, Key: "keep-history"}).Clicked() {
						a.confirmClearHistory = false
					}
					if m3.Button(c, m3.ButtonSpec{Label: "Delete all plays", Kind: m3.Filled, Size: m3.Medium56, Key: "confirm-clear-history"}).Clicked() {
						a.confirmClearHistory = false
						a.clearHistory()
					}
				})
			}
		})
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Listening stats", "Plays per day, most played tracks and artists.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Show stats", Kind: m3.Tonal, Size: m3.Medium56, Key: "show-stats"}).Clicked() {
				a.navigate(routeStats)
			}
		})
	})
}
