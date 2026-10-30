package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/offline"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// downloadTimeout bounds finding and saving one track: a long podcast
// episode takes a while even at full speed.
const downloadTimeout = 30 * time.Minute

// downloads is the music saved to play offline, and the tracks on their way
// there, one at a time. It belongs to the main thread.
type downloads struct {
	// dir is the folder that holds the files and their index; empty when
	// there is none, which turns downloading off.
	dir    string
	lib    offline.Library
	writer offline.Writer
	// queue is the tracks waiting, and active the one being downloaded.
	queue  []pendingSong
	active pendingSong
	job    task
	// version counts the changes to the library, so the Downloads page
	// knows when to read it again.
	version int
}

// pendingSong is a track waiting to be downloaded. single says the user
// asked for it by itself rather than with an album or a playlist.
type pendingSong struct {
	item   youtube.MusicItem
	single bool
}

// openDownloads reads what was downloaded into dir.
func (a *app) openDownloads(dir string) {
	a.downloads.dir = dir
	a.downloads.lib = offline.Read(dir)
	a.downloads.version++
}

// downloaded is the file of a downloaded track, or "".
func (a *app) downloaded(videoID string) string {
	d := &a.downloads
	song, ok := d.lib.Song(videoID)
	if !ok || d.dir == "" {
		return ""
	}
	return filepath.Join(d.dir, song.File)
}

// isDownloaded reports whether a track is downloaded.
func (a *app) isDownloaded(videoID string) bool {
	_, ok := a.downloads.lib.Song(videoID)
	return ok
}

// downloading reports whether a track is waiting or being downloaded.
func (a *app) downloading(videoID string) bool {
	d := &a.downloads
	if d.job.busy() && d.active.item.VideoID == videoID {
		return true
	}
	return slices.ContainsFunc(d.queue, func(p pendingSong) bool { return p.item.VideoID == videoID })
}

// canDownload reports whether a song can be downloaded or removed.
func (a *app) canDownload(item youtube.MusicItem) bool {
	return a.downloads.dir != "" && item.VideoID != "" && item.IsTrack()
}

// downloadSong saves a track to play offline.
func (a *app) downloadSong(item youtube.MusicItem) {
	a.queueDownloads([]youtube.MusicItem{item}, true)
}

// downloadCollection saves the album or playlist item names, and every track
// of it, to play offline. Tracks without artwork of their own take the
// collection's.
func (a *app) downloadCollection(item youtube.MusicItem, tracks []youtube.MusicItem) {
	d := &a.downloads
	if d.dir == "" || len(tracks) == 0 {
		return
	}
	songs := make([]youtube.MusicItem, 0, len(tracks))
	ids := make([]string, 0, len(tracks))
	for _, track := range tracks {
		if track.VideoID == "" || slices.Contains(ids, track.VideoID) {
			continue
		}
		if track.Thumbnail == "" {
			track.Thumbnail = item.Thumbnail
		}
		songs = append(songs, track)
		ids = append(ids, track.VideoID)
	}
	d.lib.AddCollection(offline.Collection{Item: item, Tracks: ids})
	a.saveDownloads()
	a.queueDownloads(songs, false)
}

// queueDownloads puts the tracks that are not downloaded yet in the queue.
func (a *app) queueDownloads(items []youtube.MusicItem, single bool) {
	d := &a.downloads
	if d.dir == "" {
		return
	}
	for _, item := range items {
		if !a.canDownload(item) {
			continue
		}
		if song, ok := d.lib.Song(item.VideoID); ok {
			if single && !song.Single {
				song.Single = true
				d.lib.Add(song)
				a.saveDownloads()
			}
			continue
		}
		if i := slices.IndexFunc(d.queue, func(p pendingSong) bool { return p.item.VideoID == item.VideoID }); i >= 0 {
			d.queue[i].single = d.queue[i].single || single
			continue
		}
		if d.job.busy() && d.active.item.VideoID == item.VideoID {
			d.active.single = d.active.single || single
			continue
		}
		d.queue = append(d.queue, pendingSong{item: item, single: single})
	}
	a.nextDownload()
}

// nextDownload starts the download of the track first in the queue, unless
// one is under way.
func (a *app) nextDownload() {
	d := &a.downloads
	if d.job.busy() || len(d.queue) == 0 {
		return
	}
	d.active = d.queue[0]
	d.queue = slices.Delete(d.queue, 0, 1)
	item, dir, streams := d.active.item, d.dir, a.streams
	startTask(a, &d.job, downloadTimeout, func(ctx context.Context) (string, error) {
		url, _, err := streams.Get(ctx, item.VideoID)
		if err != nil {
			return "", err
		}
		file, err := offline.Download(ctx, dir, item.VideoID, url)
		if err != nil {
			// A URL kept from earlier may have expired: the next try
			// resolves the track again.
			streams.Forget(item.VideoID)
		}
		return file, err
	}, func(file string, err error) {
		single := d.active.single
		d.active = pendingSong{}
		if err != nil {
			log.Printf("downloads: could not download video_id=%s: %v", item.VideoID, err)
			a.notice = "Could not download “" + item.Title + "”"
		} else {
			d.lib.Add(offline.Song{Item: item, File: file, Single: single})
			a.saveDownloads()
		}
		a.nextDownload()
	})
}

// removeDownload deletes a downloaded track, or stops it from being
// downloaded.
func (a *app) removeDownload(videoID string) {
	d := &a.downloads
	d.queue = slices.DeleteFunc(d.queue, func(p pendingSong) bool { return p.item.VideoID == videoID })
	if d.job.busy() && d.active.item.VideoID == videoID {
		d.job.stop()
		d.active = pendingSong{}
		a.nextDownload()
	}
	if file, ok := d.lib.Remove(videoID); ok {
		a.deleteDownloadFiles([]string{file})
		a.saveDownloads()
	}
}

// removeCollectionDownload deletes a downloaded album or playlist, with the
// tracks that were downloaded only for it, and stops the ones still waiting.
func (a *app) removeCollectionDownload(id string) {
	d := &a.downloads
	collection, ok := d.lib.Collection(id)
	if !ok {
		return
	}
	files := d.lib.RemoveCollection(id)
	// A track waiting only for this collection is not wanted any more.
	unwanted := func(p pendingSong) bool {
		return !p.single && slices.Contains(collection.Tracks, p.item.VideoID) && !d.lib.InCollection(p.item.VideoID)
	}
	d.queue = slices.DeleteFunc(d.queue, unwanted)
	if d.job.busy() && unwanted(d.active) {
		d.job.stop()
		d.active = pendingSong{}
		a.nextDownload()
	}
	a.deleteDownloadFiles(files)
	a.saveDownloads()
}

// collectionDownload says how much of a collection's tracks are downloaded,
// whether it was downloaded at all, and whether any of them are on their way.
func (a *app) collectionDownload(id string) (done, total int, saved, busy bool) {
	d := &a.downloads
	collection, ok := d.lib.Collection(id)
	if !ok {
		return 0, 0, false, false
	}
	for _, videoID := range collection.Tracks {
		if _, ok := d.lib.Song(videoID); ok {
			done++
		} else if a.downloading(videoID) {
			busy = true
		}
	}
	return done, len(collection.Tracks), true, busy
}

// deleteDownloadFiles deletes the files of removed tracks, off the main
// thread.
func (a *app) deleteDownloadFiles(files []string) {
	dir := a.downloads.dir
	if len(files) == 0 || dir == "" {
		return
	}
	a.run(func() {
		for _, file := range files {
			if err := os.Remove(filepath.Join(dir, file)); err != nil && !errors.Is(err, os.ErrNotExist) {
				log.Printf("downloads: removing %s: %v", file, err)
			}
		}
	})
}

// saveDownloads writes the library's index, and tells the Downloads page.
func (a *app) saveDownloads() {
	d := &a.downloads
	d.version++
	d.writer.Save(d.dir, d.lib)
}

// offlinePage is the downloaded copy of an album or a playlist, for its page
// and its play button while the network is out of reach.
func (a *app) offlinePage(id string) (*youtube.BrowseResult, bool) {
	d := &a.downloads
	collection, ok := d.lib.Collection(id)
	if !ok {
		return nil, false
	}
	return &youtube.BrowseResult{
		Title:     collection.Item.Title,
		Thumbnail: collection.Item.Thumbnail,
		Items:     d.lib.Tracks(collection),
	}, true
}

// syncDownloadsPage fills the Downloads page with what is downloaded, when
// that changed since the page last read it.
func (a *app) syncDownloadsPage() {
	d := &a.downloads
	p := a.page
	if p.downloads == d.version && !p.loadedAt.IsZero() {
		return
	}
	p.downloads, p.loadedAt, p.rowsDirty = d.version, time.Now(), true
	var sections []youtube.MusicSection
	if len(d.lib.Collections) > 0 {
		items := make([]youtube.MusicItem, len(d.lib.Collections))
		for i, collection := range d.lib.Collections {
			items[i] = collection.Item
		}
		sections = append(sections, youtube.MusicSection{Title: "Albums and playlists", Items: items})
	}
	if len(d.lib.Songs) > 0 {
		items := make([]youtube.MusicItem, len(d.lib.Songs))
		for i, song := range d.lib.Songs {
			items[i] = song.Item
		}
		sections = append(sections, youtube.MusicSection{Title: "Songs", Items: items})
	}
	p.feed = pageState{sections: sections}
}

// downloadButton is the hero's button that downloads an album or a playlist,
// says how far that has got, and removes the download again.
func (a *app) downloadButton(c *ui.Context) {
	r, id := a.route()
	if a.downloads.dir == "" || r.id != routeAlbum && r.id != routePlaylist {
		return
	}
	done, total, saved, busy := a.collectionDownload(id)
	complete := saved && done == total
	label, icon, kind := "Download", m3.IconDownload, m3.Tonal
	switch {
	case busy:
		label, kind = "Downloading "+strconv.Itoa(done)+"/"+strconv.Itoa(total), m3.Outlined
	case complete:
		label, icon, kind = "Downloaded", m3.IconDownloadDone, m3.Outlined
	}
	// A playlist's songs past its first page are still on their way while
	// the rest of it loads, and a download now would leave them out.
	disabled := !busy && !complete && (len(a.page.playable) == 0 || a.pageSimilar.busy() || a.page.feed.loading)
	if !m3.Button(c, m3.ButtonSpec{Label: label, Icon: icon, Kind: kind, Size: a.heroButtonSize(), Disabled: disabled, Key: "hero-download"}).Clicked() {
		return
	}
	if busy || complete {
		a.removeCollectionDownload(id)
		return
	}
	kindOf := youtube.KindPlaylist
	if r.id == routeAlbum {
		kindOf = youtube.KindAlbum
	}
	d := a.page.detail
	a.downloadCollection(youtube.MusicItem{BrowseID: id, Title: d.title, Subtitle: d.subtitle, Thumbnail: d.art, Kind: kindOf}, a.page.playable)
}
