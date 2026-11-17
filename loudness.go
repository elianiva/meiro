package main

import (
	"context"
	"log"
	"math"
	"time"

	"github.com/elianiva/meiro/youtube"
)

const loudnessTimeout = 15 * time.Second

func (a *app) applyLoudness() {
	value, ok := a.loudnessMap.Get(a.current.VideoID)
	a.loudness, a.loudnessOK = value.LoudnessDB, ok
	a.player.SetVolume(playbackVolume(a.volume, a.loudness, a.loudnessOK && a.settings.NormalizeLoudness, a.settings.LogVolume))
}

const loudnessWait = 2 * time.Second

func (a *app) loudnessClient(videoID string) *youtube.Client {
	if a.account.session == nil || !a.settings.NormalizeLoudness {
		return nil
	}
	if _, ok := a.loudnessMap.Get(videoID); ok {
		return nil
	}
	return a.account.session.client
}

func (a *app) fetchLoudness(item youtube.MusicItem, load *task) {
	client := a.loudnessClient(item.VideoID)
	if client == nil {
		return
	}
	startTask(a, load, loudnessTimeout, func(ctx context.Context) (youtube.Loudness, error) {
		return client.Loudness(ctx, item.VideoID)
	}, func(value youtube.Loudness, err error) {
		if err != nil {
			log.Printf("playback: loudness unavailable video_id=%s: %v", item.VideoID, err)
			return
		}
		a.loudnessMap.Put(item.VideoID, value)
		if a.current.VideoID == item.VideoID {
			a.applyLoudness()
			a.syncSystemMedia()
		}
	})
}

const (
	decibelsPerDecade = 20
	logVolumeDecades  = 3
)

func playbackVolume(volume, loudness float64, normalize, logarithmic bool) float64 {
	volume /= 100
	if logarithmic {
		volume = (math.Pow(10, logVolumeDecades*volume) - 1) / (math.Pow(10, logVolumeDecades) - 1)
	}
	gain := 1.0
	if normalize {
		gain = math.Pow(10, -loudness/decibelsPerDecade)
	}
	return min(max(volume*gain, 0), 1)
}

func (a *app) setNormalizeLoudness(enabled bool) {
	if a.settings.NormalizeLoudness == enabled {
		return
	}
	a.settings.NormalizeLoudness = enabled
	a.saveSettings()
	a.applyLoudness()
	if enabled && a.current.VideoID != "" {
		a.fetchLoudness(a.current, &a.loudnessLoad)
	}
}
