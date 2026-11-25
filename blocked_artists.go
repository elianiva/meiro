package main

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const blockedReason = "You blocked this song's artist."

// artistBlocks reports whether the song is by a blocked artist. Only the
// song's first credited artist counts: a blocked artist who only features on
// a song does not stop it from playing.
func (a *app) artistBlocks(item youtube.MusicItem) bool {
	if len(a.settings.BlockedArtists) == 0 || !isMusicTrack(item) {
		return false
	}
	id, _ := item.PrimaryArtist()
	return a.settings.ArtistBlocked(id)
}

func (a *app) artistIsBlocked(id string) bool { return a.settings.ArtistBlocked(id) }

// blockedArtistName is what the artist is called in messages and in the list
// of blocked artists.
func blockedArtistName(name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return "this artist"
}

func (a *app) blockArtist(id, name string) {
	if id == "" || a.artistIsBlocked(id) {
		return
	}
	name = strings.TrimSpace(name)
	a.settings.BlockedArtists = append(a.settings.BlockedArtists, settings.BlockedArtist{ID: id, Name: name})
	a.saveSettings()
	a.blockedArtistsChanged()
	a.notice = "Blocked " + blockedArtistName(name)
}

func (a *app) unblockArtist(id string) {
	index := slices.IndexFunc(a.settings.BlockedArtists, func(b settings.BlockedArtist) bool { return b.ID == id })
	if index < 0 {
		return
	}
	name := a.settings.BlockedArtists[index].Name
	a.settings.BlockedArtists = slices.Delete(a.settings.BlockedArtists, index, index+1)
	a.saveSettings()
	a.blockedArtistsChanged()
	a.notice = "Unblocked " + blockedArtistName(name)
}

// blockedArtistsChanged applies the list as it now is: songs by a blocked
// artist leave the queue and the lists, and the song playing is skipped if
// its artist was just blocked.
func (a *app) blockedArtistsChanged() {
	a.blockedVersion++
	a.applyAIFilterChange()
	if a.current.VideoID != "" && a.artistBlocks(a.current) {
		a.skipExcluded(a.current)
	}
}

func (a *app) blockArtistButton(c *ui.Context, r *route, id string) {
	if r.target != youtube.TargetArtist || id == "" {
		return
	}
	label, icon, kind := "Block", m3.IconBlock, m3.Outlined
	if a.artistIsBlocked(id) {
		label, kind = "Unblock", m3.Tonal
	}
	if m3.Button(c, m3.ButtonSpec{Label: label, Icon: icon, Kind: kind, Size: a.heroButtonSize(), Key: "hero-block"}).Clicked() {
		if a.artistIsBlocked(id) {
			a.unblockArtist(id)
			return
		}
		a.blockArtist(id, a.page.detail.title)
	}
}

func (a *app) blockedArtistsCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Blocked artists", m3.IconBlock, func() {
		settingLabel(c, "Skip these artists",
			"Meiro skips their songs and hides them from lists and recommendations. Songs that only feature them still play. Block an artist from a song's menu or from their page.")
		if len(a.settings.BlockedArtists) == 0 {
			m3.Text(c, m3.BodyMedium, "No blocked artists.").TextColor(sc.OnSurfaceVariant)
			return
		}
		for _, artist := range slices.Clone(a.settings.BlockedArtists) {
			ui.Row(c.Key("blocked-" + artist.ID)).Gap(12).AlignItems(ui.Center).Children(func() {
				m3.Text(c, m3.BodyLarge, cmp.Or(artist.Name, artist.ID)).SingleLine().Grow(1).MinWidth(0).TextColor(sc.OnSurface)
				if m3.Button(c, m3.ButtonSpec{Label: "Unblock", Kind: m3.Outlined, Size: m3.Small40, Key: "unblock-" + artist.ID}).Clicked() {
					a.unblockArtist(artist.ID)
				}
			})
		}
	})
}
