package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/elianiva/meiro/youtube"
)

// playAll plays everything on the page shown.
func (a *app) playAll() {
	if len(a.page.playable) == 0 {
		return
	}
	options, source := a.queueContext(0)
	a.playFrom(a.page.playable[0], a.page.playable, 0, options, source)
}

// shufflePage turns shuffle on and plays the page from a track drawn at
// random, with the whole page arranged behind it.
func (a *app) shufflePage() {
	if len(a.page.playable) == 0 {
		return
	}
	a.shuffle = true
	// The position the playlist is read from goes with the draw, so the queue
	// YouTube is asked for follows the same track.
	start, tracks := drawFirst(a.page.playable)
	options, source := a.queueContext(start)
	a.playFrom(tracks[0], tracks, 0, options, source)
}

// startRadioPage plays a track drawn at random from the page shown and lets
// YouTube Music generate the queue behind it, as the hero's Start radio does.
func (a *app) startRadioPage() {
	if len(a.page.playable) == 0 {
		return
	}
	seed := a.page.playable[rand.IntN(len(a.page.playable))]
	_, source := a.queueContext(0)
	a.playFrom(seed, nil, 0, youtube.UpNextOptions{}, source)
}

// drawFirst draws a track at random to open a queue on, with the rest
// shuffled behind it, and says where the drawn track was.
func drawFirst(tracks []youtube.MusicItem) (int, []youtube.MusicItem) {
	start := rand.IntN(len(tracks))
	out := make([]youtube.MusicItem, 0, len(tracks))
	out = append(out, tracks[start])
	out = append(out, tracks[:start]...)
	out = append(out, tracks[start+1:]...)
	rest := out[1:]
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return start, out
}

// queueContext is what playing from the page shown tells YouTube Music about
// the queue, and the name of the page, for the queue's heading. A playlist
// page adds where in the playlist the queue starts.
func (a *app) queueContext(index int) (youtube.UpNextOptions, string) {
	options := youtube.UpNextOptions{}
	r, id := a.route()
	if r.target == youtube.TargetPlaylist {
		playlistIndex := index
		options.PlaylistID, options.PlaylistIndex = id, &playlistIndex
	}
	source := a.page.detail.title
	if source == "" {
		source = r.source
	}
	return options, source
}

// openTimeout bounds reading an album or a playlist to play it.
const openTimeout = time.Minute

// playCollection plays an album or a playlist without opening its page, as
// the play button over its card does. With shuffle, a song is drawn to open
// on and the rest are arranged behind it. key names the card pressed.
func (a *app) playCollection(item youtube.MusicItem, key string, shuffle bool) {
	if shuffle {
		a.shuffle = true
	}
	target := item.Target()
	if target.Kind == youtube.TargetTrack {
		a.play(item, []youtube.MusicItem{item})
		return
	}
	a.withCollectionTracks(item, key, func(songs []youtube.MusicItem) {
		// What the playlist is read from follows the song drawn to open it,
		// so the queue YouTube is asked for keeps the same place.
		start := 0
		if shuffle && len(songs) > 1 {
			start, songs = drawFirst(songs)
		}
		options := youtube.UpNextOptions{}
		if target.Kind == youtube.TargetPlaylist {
			playlistIndex := start
			options.PlaylistID, options.PlaylistIndex = target.ID, &playlistIndex
		}
		a.playFrom(songs[0], songs, 0, options, item.Title)
	})
}

// startRadio plays a seed from what the item names, either the track itself or one
// drawn from the album, the playlist or the artist, and lets YouTube Music
// generate the queue behind it, as the radio and mix entries of its menus do.
func (a *app) startRadio(item youtube.MusicItem, key string) {
	if target := item.Target(); target.Kind == youtube.TargetTrack {
		a.playFrom(item, nil, 0, youtube.UpNextOptions{}, item.Title)
		return
	}
	a.withCollectionTracks(item, key, func(songs []youtube.MusicItem) {
		seed := songs[rand.IntN(len(songs))]
		a.playFrom(seed, nil, 0, youtube.UpNextOptions{}, item.Title)
	})
}

// withCollectionTracks reads the page of the album, the playlist or the artist
// an item names and hands its tracks to then, keeping a second press on the
// same card from reading it again.
func (a *app) withCollectionTracks(item youtube.MusicItem, key string, then func([]youtube.MusicItem)) {
	target := item.Target()
	if target.Kind != youtube.TargetAlbum && target.Kind != youtube.TargetPlaylist && target.Kind != youtube.TargetArtist {
		return
	}
	if a.isOpening(key) {
		return
	}
	a.openingKey = key
	client := a.client()
	startTask(a, &a.opening, openTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		switch target.Kind {
		case youtube.TargetAlbum:
			return client.GetAlbum(ctx, target.ID)
		case youtube.TargetArtist:
			return client.GetArtist(ctx, target.ID)
		case youtube.TargetPlaylist, youtube.TargetTrack, youtube.TargetPodcast, youtube.TargetNone:
			// The guard above leaves the playlist of the kinds that are not
			// its own.
		}
		// The first page of a playlist holds the playlist itself. Draining
		// it reads on into the shelf of suggestions beside it, which never
		// ends, and long outlasts the read: the page reads it the same way.
		return client.GetPlaylist(ctx, target.ID)
	}, func(result *youtube.BrowseResult, err error) {
		a.openingKey = ""
		if downloaded, ok := a.offlinePage(target.ID); ok && errors.Is(err, youtube.ErrUnreachable) {
			result, err = downloaded, nil
		}
		if err != nil {
			a.notice = "Could not open “" + item.Title + "”: " + firstLine(err.Error())
			return
		}
		songs := tracksOf(result, item.Thumbnail)
		if len(songs) == 0 {
			return
		}
		then(songs)
	})
}

// isOpening reports whether the card under key is being read to play.
func (a *app) isOpening(key string) bool { return a.opening.busy() && a.openingKey == key }

// tracksOf picks the tracks out of a page, in order, giving those without
// artwork of their own the page's.
func tracksOf(result *youtube.BrowseResult, art string) []youtube.MusicItem {
	all := result.Items
	if len(result.Sections) > 0 {
		all = nil
		for _, section := range result.Sections {
			all = append(all, section.Items...)
		}
	}
	var songs []youtube.MusicItem
	for _, song := range all {
		if !song.IsTrack() {
			continue
		}
		if song.Thumbnail == "" {
			song.Thumbnail = art
		}
		songs = append(songs, song)
	}
	return songs
}
