package player

import (
	"bytes"
	"io"
	"math"
	"testing"
	"time"
)

func TestSetEQChangesFilterWithoutChangingTheDecoder(t *testing.T) {
	p, wav := newTestPlayerWithDuration(t, 10)
	if err := p.Play(wav); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the decoder to produce audio", func() bool { return p.Position() > 0 })
	if err := p.Preload("next", wav); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	session := p.session
	process := session.cmd.Process
	next := p.next
	preloadedTarget := next.eq.target.Load()
	position := session.played()
	p.mu.Unlock()

	gains := make([]float64, EQBandCount)
	gains[4] = 6
	p.SetEQ(gains)

	p.mu.Lock()
	unchanged := p.session == session && p.session.cmd.Process == process && p.next == next
	updatedPreload := next.eq.target.Load() != preloadedTarget
	p.mu.Unlock()
	if !unchanged {
		t.Fatal("changing EQ replaced the playing or preloaded decoder")
	}
	if !updatedPreload {
		t.Fatal("changing EQ did not update the preloaded stream")
	}
	if !p.Playing() {
		t.Fatal("changing EQ stopped playback")
	}
	waitFor(t, "playback to continue past the EQ change", func() bool { return p.Position() > position+100*time.Millisecond })
	if p.Failure() != "" {
		t.Fatalf("the stream failed after changing EQ: %s", p.Failure())
	}
}

func TestEqualizerShapesTheChosenFrequency(t *testing.T) {
	flat := make([]float64, EQBandCount)
	boosted := make([]float64, EQBandCount)
	boosted[4] = 6 // 500 Hz
	base := sinePCM(500, time.Second)
	nearby := sinePCM(2000, time.Second)
	flatOutput := filteredPCM(t, base, flat)
	boostedOutput := filteredPCM(t, base, boosted)
	nearbyOutput := filteredPCM(t, nearby, boosted)
	if got, want := rmsPCM(boostedOutput), rmsPCM(flatOutput); math.Abs(got-want) > want*0.12 {
		t.Errorf("headroom-compensated 500 Hz RMS = %v, flat = %v", got, want)
	}
	if target, other := rmsPCM(boostedOutput), rmsPCM(nearbyOutput); target < other*1.8 {
		t.Errorf("boosted 500 Hz RMS = %v, 2 kHz RMS = %v, want the selected band to stand out", target, other)
	}
}

func TestEQChangeBlendsAndUpdatesAnExistingReader(t *testing.T) {
	data := make([]byte, sampleRate*channelCount*bytesPerSample)
	eq := newEqualizer(nil)
	reader := newEQReader(bytes.NewReader(data), eq)
	if _, err := io.ReadFull(reader, make([]byte, len(data)/2)); err != nil {
		t.Fatal(err)
	}
	gains := make([]float64, EQBandCount)
	gains[4] = 6
	eq.Set(gains)
	reader.refreshTarget()
	if reader.remaining != eqTransitionFrames {
		t.Fatalf("transition frames = %d, want %d", reader.remaining, eqTransitionFrames)
	}
	reader.process(make([]byte, eqBlockFrames*4))
	if reader.remaining != eqTransitionFrames-eqBlockFrames {
		t.Errorf("transition frames after one block = %d", reader.remaining)
	}
}

func TestStreamArgsLeavesEQOutOfTheDecoder(t *testing.T) {
	args := streamArgs("https://example.invalid/audio", 0, true)
	for _, arg := range args {
		if arg == "-af" {
			t.Fatalf("streamArgs = %v, want EQ to run outside ffmpeg", args)
		}
	}
	if got := args[len(args)-1]; got != "pipe:1" {
		t.Errorf("last argument = %q, want the output pipe", got)
	}
}

func sinePCM(frequency float64, duration time.Duration) []byte {
	frames := int(duration.Seconds() * sampleRate)
	pcm := make([]byte, frames*channelCount*bytesPerSample)
	for i := range frames {
		sample := int16(math.Sin(2*math.Pi*frequency*float64(i)/sampleRate) * 12000)
		for channel := range channelCount {
			offset := (i*channelCount + channel) * bytesPerSample
			pcm[offset], pcm[offset+1] = byte(sample), byte(sample>>8)
		}
	}
	return pcm
}

func filteredPCM(t *testing.T, input []byte, gains []float64) []byte {
	t.Helper()
	output := make([]byte, len(input))
	reader := newEQReader(bytes.NewReader(input), newEqualizer(gains))
	if _, err := io.ReadFull(reader, output); err != nil {
		t.Fatal(err)
	}
	return output
}

func rmsPCM(pcm []byte) float64 {
	start := sampleRate / 5 * channelCount * bytesPerSample
	var sum float64
	count := 0
	for offset := start; offset+1 < len(pcm); offset += bytesPerSample {
		sample := int16(uint16(pcm[offset]) | uint16(pcm[offset+1])<<8)
		value := float64(sample) / 32768
		sum += value * value
		count++
	}
	return math.Sqrt(sum / float64(count))
}
