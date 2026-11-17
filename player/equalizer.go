package player

import (
	"io"
	"math"
	"sync/atomic"
)

const (
	eqTransitionFrames = sampleRate / 20
	eqBlockFrames      = 128
)

type biquad struct {
	b0, b1, b2 float64
	a1, a2     float64
}

type eqConfig struct {
	bands [EQBandCount]biquad
	gain  float64
}

type equalizer struct {
	target atomic.Pointer[eqConfig]
}

func newEqualizer(gains []float64) *equalizer {
	e := &equalizer{}
	e.Set(gains)
	return e
}

func (e *equalizer) Set(gains []float64) {
	config := &eqConfig{gain: 1}
	for i := range config.bands {
		config.bands[i] = biquad{b0: 1}
	}
	if len(gains) == EQBandCount {
		boost := 0.0
		for i, gain := range gains {
			config.bands[i] = peakingEQ(EQFrequencies[i], gain)
			boost = max(boost, gain)
		}
		config.gain = math.Pow(10, -boost/20)
	}
	e.target.Store(config)
}

func peakingEQ(frequency, gainDB float64) biquad {
	if gainDB == 0 {
		return biquad{b0: 1}
	}
	a := math.Pow(10, gainDB/40)
	w := 2 * math.Pi * frequency / sampleRate
	alpha := math.Sin(w) / (2 * 1.4)
	cos := math.Cos(w)
	a0 := 1 + alpha/a
	return biquad{
		b0: (1 + alpha*a) / a0,
		b1: (-2 * cos) / a0,
		b2: (1 - alpha*a) / a0,
		a1: (-2 * cos) / a0,
		a2: (1 - alpha/a) / a0,
	}
}

type filterState struct {
	z1, z2 [channelCount]float64
}

type eqReader struct {
	reader io.Reader
	eq     *equalizer

	config    *eqConfig
	coeffs    [EQBandCount]biquad
	steps     [EQBandCount]biquad
	gain      float64
	gainStep  float64
	remaining int
	states    [EQBandCount]filterState

	pending []byte
	ready   []byte
	scratch [32 * 1024]byte
	readErr error
}

func newEQReader(reader io.Reader, eq *equalizer) *eqReader {
	config := eq.target.Load()
	return &eqReader{reader: reader, eq: eq, config: config, coeffs: config.bands, gain: config.gain}
}

func (r *eqReader) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	if len(r.ready) > 0 {
		return r.readReady(out)
	}
	wanted := min(max(len(out)-len(out)%bytesPerFrame, bytesPerFrame), len(r.scratch))
	for len(r.pending) < wanted && r.readErr == nil {
		need := min(wanted-len(r.pending)+bytesPerFrame-1, len(r.scratch))
		n, err := r.reader.Read(r.scratch[:need])
		r.pending = append(r.pending, r.scratch[:n]...)
		if err != nil {
			r.readErr = err
		}
		if n == 0 && err == nil {
			return 0, nil
		}
	}
	n := min(len(r.pending), wanted)
	n -= n % bytesPerFrame
	if n > 0 {
		r.refreshTarget()
		r.process(r.pending[:n])
	} else if r.readErr != nil {
		n = min(len(r.pending), len(out))
	}
	r.ready = r.pending[:n]
	r.pending = r.pending[n:]
	return r.readReady(out)
}

func (r *eqReader) readReady(out []byte) (int, error) {
	n := copy(out, r.ready)
	r.ready = r.ready[n:]
	if n == 0 {
		if r.readErr != nil {
			return 0, r.readErr
		}
		return 0, nil
	}
	return n, nil
}

func (r *eqReader) refreshTarget() {
	target := r.eq.target.Load()
	if target == r.config {
		return
	}
	r.config = target
	r.remaining = eqTransitionFrames
	r.gainStep = (target.gain - r.gain) / float64(eqTransitionFrames)
	for i, next := range target.bands {
		current := r.coeffs[i]
		r.steps[i] = biquad{
			b0: (next.b0 - current.b0) / float64(eqTransitionFrames),
			b1: (next.b1 - current.b1) / float64(eqTransitionFrames),
			b2: (next.b2 - current.b2) / float64(eqTransitionFrames),
			a1: (next.a1 - current.a1) / float64(eqTransitionFrames),
			a2: (next.a2 - current.a2) / float64(eqTransitionFrames),
		}
	}
}

func (r *eqReader) process(pcm []byte) {
	for frame := 0; frame < len(pcm)/bytesPerFrame; {
		frames := min(eqBlockFrames, len(pcm)/bytesPerFrame-frame)
		if r.remaining > 0 {
			frames = min(frames, r.remaining)
			for i := range r.coeffs {
				r.coeffs[i].b0 += r.steps[i].b0 * float64(frames)
				r.coeffs[i].b1 += r.steps[i].b1 * float64(frames)
				r.coeffs[i].b2 += r.steps[i].b2 * float64(frames)
				r.coeffs[i].a1 += r.steps[i].a1 * float64(frames)
				r.coeffs[i].a2 += r.steps[i].a2 * float64(frames)
			}
			r.gain += r.gainStep * float64(frames)
		}
		for i := range frames {
			for channel := range channelCount {
				offset := (frame+i)*bytesPerFrame + channel*bytesPerSample
				sample := int16(uint16(pcm[offset]) | uint16(pcm[offset+1])<<8)
				value := float64(sample) / 32768
				for band, coeff := range r.coeffs {
					state := &r.states[band]
					output := coeff.b0*value + state.z1[channel]
					state.z1[channel] = coeff.b1*value - coeff.a1*output + state.z2[channel]
					state.z2[channel] = coeff.b2*value - coeff.a2*output
					value = output
				}
				value *= r.gain
				scaled := int(math.Round(value * 32768))
				scaled = min(max(scaled, math.MinInt16), math.MaxInt16)
				pcm[offset], pcm[offset+1] = byte(scaled), byte(scaled>>8)
			}
		}
		frame += frames
		if r.remaining > 0 {
			r.remaining -= frames
			if r.remaining == 0 {
				r.coeffs = r.config.bands
				r.gain = r.config.gain
			}
		}
	}
}
