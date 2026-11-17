package main

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/lru"
	"github.com/elianiva/meiro/youtube"
)

const (
	aiFilterCacheLimit = 512
	aiLookupTimeout    = 10 * time.Second
	maxAIRefills       = 3
)

type aiFilterState struct {
	mu      sync.Mutex
	cache   *lru.Cache[string, youtube.AIDisclosure]
	version uint64
}

func newAIFilter() aiFilterState {
	return aiFilterState{cache: lru.New[string, youtube.AIDisclosure](aiFilterCacheLimit)}
}

func (s *aiFilterState) get(videoID string) (youtube.AIDisclosure, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		return youtube.AIUnknown, false
	}
	return s.cache.Get(videoID)
}

func (s *aiFilterState) put(videoID string, status youtube.AIDisclosure) bool {
	if videoID == "" || status == youtube.AIUnknown {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache == nil {
		s.cache = lru.New[string, youtube.AIDisclosure](aiFilterCacheLimit)
	}
	if old, ok := s.cache.Peek(videoID); ok && old == status {
		return false
	}
	s.cache.Put(videoID, status)
	s.version++
	return true
}

func (s *aiFilterState) generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

func isMusicTrack(item youtube.MusicItem) bool {
	return item.Target().Kind == youtube.TargetTrack && !item.IsEpisode()
}

func (a *app) aiDisclosure(item youtube.MusicItem) youtube.AIDisclosure {
	if item.AIDisclosure != youtube.AIUnknown {
		return item.AIDisclosure
	}
	if status, ok := a.aiFilter.get(item.VideoID); ok {
		return status
	}
	return youtube.AIUnknown
}

func (a *app) excludesItem(item youtube.MusicItem) bool {
	return a.settings.ExcludeAIGenerated && isMusicTrack(item) &&
		a.aiDisclosure(item) == youtube.AIAlteredOrGenerated
}

func (a *app) visibleItems(items []youtube.MusicItem) []youtube.MusicItem {
	if !a.settings.ExcludeAIGenerated {
		return items
	}
	kept := make([]youtube.MusicItem, 0, len(items))
	for _, item := range items {
		if !a.excludesItem(item) {
			kept = append(kept, item)
		}
	}
	return kept
}

func (a *app) rememberAIDisclosure(videoID string, status youtube.AIDisclosure) {
	if a.aiFilter.put(videoID, status) {
		a.page.rowsDirty = true
	}
	if status == youtube.AIUnknown {
		return
	}
	a.queue.each(func(item *youtube.MusicItem) {
		if item.VideoID == videoID {
			item.AIDisclosure = status
		}
	})
	if a.current.VideoID == videoID {
		a.current.AIDisclosure = status
	}
}

func (a *app) setExcludeAIGenerated(enabled bool) {
	if a.settings.ExcludeAIGenerated == enabled {
		return
	}
	a.settings.ExcludeAIGenerated = enabled
	a.saveSettings()
	a.applyAIFilterChange()
}

func (a *app) applyAIFilterChange() {
	if a.settings.ExcludeAIGenerated {
		a.queue.dropMarkedAI(a.excludesItem)
	} else if a.playErr == aiExcludedReason {
		a.playErr, a.rawPlayErr = "", ""
	}
	a.page.rowsDirty = true
	a.queueChanged()
	a.refresh()
}

func (a *app) checkAIBeforePlay(item youtube.MusicItem, startAt time.Duration, changed bool) bool {
	if a.excludesItem(item) {
		a.skipExcluded(item)
		return true
	}
	if !a.settings.ExcludeAIGenerated || !isMusicTrack(item) || a.aiDisclosure(item) != youtube.AIUnknown {
		return false
	}
	client := a.client()
	videoID := item.VideoID
	startTask(a, &a.aiCheck, aiLookupTimeout, func(ctx context.Context) (youtube.AIDisclosure, error) {
		if err := ctx.Err(); err != nil {
			return youtube.AIUnknown, err
		}
		a.player.Stop()
		return client.AIDisclosure(ctx, videoID)
	}, func(status youtube.AIDisclosure, err error) {
		if err != nil {
			log.Printf("playback: AI disclosure unavailable video_id=%s: %v", videoID, err)
			status = youtube.AIUnknown
		}
		a.rememberAIDisclosure(videoID, status)
		if a.settings.ExcludeAIGenerated && status == youtube.AIAlteredOrGenerated {
			a.skipExcluded(item)
			return
		}
		a.startTrackNow(item, startAt, changed)
	})
	return true
}

const aiExcludedReason = "YouTube marks this song as made with AI."

func (a *app) skipExcluded(item youtube.MusicItem) {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "This song"
	}
	a.notice = "Skipped " + title + ": YouTube marks it as made with AI"
	for range a.queue.len() {
		next, ok := a.queue.next(a.settings.AutoPlay, a.repeat)
		if !ok {
			if a.settings.AutoPlay && a.aiRefills < maxAIRefills {
				a.aiRefills++
				a.playErr, a.rawPlayErr = aiExcludedReason, aiExcludedReason
				a.upNext.waiting = true
				a.finishEpisodeProgress(false)
				a.player.Stop()
				a.syncSystemMedia()
				a.ensureUpNext()
				return
			}
			break
		}
		candidate := a.queue.moveTo(next)
		if !a.excludesItem(candidate) {
			a.startTrack(candidate, true)
			return
		}
	}
	a.current = a.queue.current()
	a.total = parseDuration(a.current.Duration)
	a.scrub = 0
	a.playErr, a.rawPlayErr = aiExcludedReason, aiExcludedReason
	a.stopAtQueueEnd()
}
