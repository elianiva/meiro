package main

import (
	"log"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

const releasesURL = "https://github.com/elianiva/meiro/releases"

type toolHelpState struct {
	open bool
}

func (a *app) openToolHelp() {
	a.toolHelp = &toolHelpState{open: true}
	a.loadToolVersions()
}

func (a *app) toolHelpDialog(c *ui.Context) {
	d := a.toolHelp
	if d == nil {
		return
	}
	if !d.open {
		a.toolHelp = nil
		return
	}
	sc := m3.Of(c).Scheme
	full := a.ytdlpBundled()
	version := "Looking up…"
	if a.toolsLoaded {
		version = a.tools.YtDlp
	}
	m3.Dialog(c, &d.open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "yt-dlp may be out of date")
		m3.Text(c, m3.BodyMedium, "Meiro uses yt-dlp to find the audio of each track. YouTube changes often, and an old yt-dlp stops getting audio for some tracks. That is what “Requested format is not available” usually means.").
			TextColor(sc.OnSurfaceVariant)
		ui.Column(c).Padding(12, 16).Gap(4).Radius(m3.Large).Background(sc.SurfaceContainerHighest).Children(func() {
			if full {
				m3.EmphasizedText(c, m3.LabelLarge, "You use the full build")
				m3.Text(c, m3.BodyMedium, "It includes yt-dlp "+version+". Every release of Meiro ships the newest yt-dlp, so install the latest release.").TextColor(sc.OnSurface)
			} else {
				m3.EmphasizedText(c, m3.LabelLarge, "You use the minimal build")
				m3.Text(c, m3.BodyMedium, "It uses yt-dlp "+version+" from your PATH. Update it yourself, for example with “yt-dlp -U” or your package manager. Or change to the full build, which includes the newest yt-dlp.").TextColor(sc.OnSurface)
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Open releases page", Kind: m3.TextOnly, Key: "open-releases"}).Clicked() {
				if err := a.openURL(releasesURL); err != nil {
					log.Printf("opening the releases page: %v", err)
					a.notice = "Could not open the releases page: " + err.Error()
				}
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Close", Key: "close-tool-help"}).Clicked() {
				a.toolHelp = nil
			}
		})
	})
}

func (a *app) ytdlpBundled() bool { return a.bundled("yt-dlp") }
