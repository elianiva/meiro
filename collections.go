package main

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/elianiva/meiro/youtube"
)

func (a *app) artistSongs() (youtube.MusicItem, bool) {
	if r, _ := a.route(); r.target != youtube.TargetArtist {
		return youtube.MusicItem{}, false
	}
	for _, section := range a.page.feed.sections {
		if section.MoreID != "" {
			return youtube.MusicItem{PlaylistID: section.MoreID, Title: a.page.detail.title}, true
		}
	}
	return youtube.MusicItem{}, false
}

func (a *app) playShelf(r row) {
	if len(r.queue) == 0 {
		return
	}
	options, source := a.queueContext(0)
	if r.title != "" {
		source = r.title
	}
	a.playFrom(r.queue[0], r.queue, 0, options, source)
}

func (a *app) playAll() {
	if list, ok := a.artistSongs(); ok {
		a.playCollection(list, "hero-play", false)
		return
	}
	if len(a.page.playable) == 0 {
		return
	}
	options, source := a.queueContext(0)
	a.playFrom(a.page.playable[0], a.page.playable, 0, options, source)
}

func (a *app) shufflePage() {
	if list, ok := a.artistSongs(); ok {
		a.playCollection(list, "hero-shuffle", true)
		return
	}
	if len(a.page.playable) == 0 {
		return
	}
	a.shuffle = true
	start, tracks := drawFirst(a.page.playable)
	options, source := a.queueContext(start)
	a.playFrom(tracks[0], tracks, 0, options, source)
}

func (a *app) startRadioPage() {
	if len(a.page.playable) == 0 {
		return
	}
	seed := a.page.playable[rand.IntN(len(a.page.playable))]
	_, source := a.queueContext(0)
	a.playFrom(seed, nil, 0, youtube.UpNextOptions{}, source)
}

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

const openTimeout = time.Minute

func (a *app) playCollection(item youtube.MusicItem, key string, shuffle bool) {
	if shuffle {
		a.shuffle = true
	}
	target := item.Target()
	if target.Kind == youtube.TargetTrack {
		a.play(item, []youtube.MusicItem{item})
		return
	}
	a.withCollectionTracks(item, key, false, func(songs []youtube.MusicItem, token string) {
		start := 0
		if shuffle && len(songs) > 1 {
			start, songs = drawFirst(songs)
		}
		options := youtube.UpNextOptions{}
		if target.Kind == youtube.TargetPlaylist {
			playlistIndex := start
			options.PlaylistID, options.PlaylistIndex = target.ID, &playlistIndex
		}
		a.playFromWithContinuation(songs[0], songs, 0, options, item.Title, token)
	})
}

func (a *app) startRadio(item youtube.MusicItem, key string) {
	if target := item.Target(); target.Kind == youtube.TargetTrack {
		a.playFrom(item, nil, 0, youtube.UpNextOptions{}, item.Title)
		return
	}
	a.withCollectionTracks(item, key, false, func(songs []youtube.MusicItem, _ string) {
		seed := songs[rand.IntN(len(songs))]
		a.playFrom(seed, nil, 0, youtube.UpNextOptions{}, item.Title)
	})
}

func (a *app) withCollectionTracks(item youtube.MusicItem, key string, all bool, then func([]youtube.MusicItem, string)) {
	target := item.Target()
	if target.Kind != youtube.TargetAlbum && target.Kind != youtube.TargetPlaylist && target.Kind != youtube.TargetArtist {
		return
	}
	if a.isOpening(key) {
		return
	}
	a.openingKey = key
	client := a.client()
	artistList := ""
	startTask(a, &a.opening, openTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		switch target.Kind {
		case youtube.TargetAlbum:
			return client.GetAlbum(ctx, target.ID)
		case youtube.TargetArtist:
			page, err := client.GetArtist(ctx, target.ID)
			if err != nil {
				return page, err
			}
			for _, section := range page.Sections {
				if section.MoreID == "" {
					continue
				}
				if songs, err := client.GetPlaylist(ctx, section.MoreID); err == nil && len(tracksOf(songs, "")) > 0 {
					artistList = section.MoreID
					return songs, nil
				}
				break
			}
			return page, nil
		case youtube.TargetPlaylist, youtube.TargetTrack, youtube.TargetPodcast, youtube.TargetNone:
		}
		result, err := client.GetPlaylist(ctx, target.ID)
		if err != nil || !all {
			return result, err
		}
		token, _ := playlistContinuations(result.Sections, result.ContinuationToken)
		more, err := readPlaylistRest(ctx, token, client.ContinueBrowse)
		if err != nil {
			return nil, err
		}
		if len(result.Sections) > 0 {
			result.Sections = append(result.Sections, youtube.MusicSection{Items: more.items})
		} else {
			result.Items = append(result.Items, more.items...)
		}
		return result, nil
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
		token := ""
		if (target.Kind == youtube.TargetPlaylist || artistList != "") && !all {
			token, _ = playlistContinuations(result.Sections, result.ContinuationToken)
		}
		then(songs, token)
	})
}

func (a *app) isOpening(key string) bool { return a.opening.busy() && a.openingKey == key }

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

func (a *app) loadPlayingPlaylistRest() {
	token := a.queue.playlistMore
	if token == "" || a.queueRest.busy() {
		return
	}
	client := a.client()
	startTask(a, &a.queueRest, openTimeout, func(ctx context.Context) (moreItems, error) {
		return readPlaylistRest(ctx, token, client.ContinueBrowse)
	}, func(more moreItems, err error) {
		a.queue.playlistMore = ""
		if err != nil {
			a.notice = "Could not load the rest of the playlist: " + firstLine(err.Error())
			a.ensureUpNext()
			return
		}
		a.queue.appendPlaylist(a.visibleItems(more.items), a.shuffle)
		a.clearQueueSelection()
		a.warmNext()
		a.ensureUpNext()
	})
}
