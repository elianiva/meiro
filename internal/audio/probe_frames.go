package audio

import (
	"context"
	"os"
	"time"
)

const (
	id3HeaderSize = 10
	id3FooterSize = 10
)

const (
	layerI   = 3
	layerII  = 2
	layerIII = 1
)

const mpeg1 = 3

func skipID3(f *os.File, size int64) int64 {
	var header [id3HeaderSize]byte
	if size >= int64(len(header)) && readAt(f, 0, header[:]) && string(header[:3]) == "ID3" {
		for _, b := range header[6:10] {
			if b&0x80 != 0 {
				return 0
			}
		}
		length := int64(header[6])<<21 | int64(header[7])<<14 | int64(header[8])<<7 | int64(header[9])
		if header[5]&0x10 != 0 {
			length += id3FooterSize
		}
		if length+id3HeaderSize > size {
			return 0
		}
		return length + id3HeaderSize
	}
	return 0
}

func probeAAC(ctx context.Context, f *os.File, size int64) (time.Duration, bool) {
	offset := skipID3(f, size)
	var totalSamples uint64
	var sampleRate uint32
	frames := 0
	for offset+7 <= size {
		if frames&255 == 0 && ctx.Err() != nil {
			return 0, true
		}
		var h [7]byte
		if !readAt(f, offset, h[:]) {
			break
		}
		if h[0] != 0xff || h[1]&0xf6 != 0xf0 {
			if frames == 0 {
				return 0, false
			}
			break
		}
		frequencyIndex := (h[2] >> 2) & 0x0f
		if frequencyIndex >= 13 {
			return 0, frames > 0
		}
		rates := [...]uint32{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}
		rate := rates[frequencyIndex]
		frameLength := int64(h[3]&0x03)<<11 | int64(h[4])<<3 | int64(h[5]>>5)
		headerLength := int64(7)
		if h[1]&1 == 0 {
			headerLength += 2
		}
		if frameLength < headerLength || offset+frameLength > size {
			return 0, frames > 0
		}
		if sampleRate != 0 && sampleRate != rate {
			return 0, true
		}
		sampleRate = rate
		totalSamples += 1024 * uint64((h[6]&0x03)+1)
		frames++
		offset += frameLength
	}
	if frames == 0 {
		return 0, false
	}
	return durationFromSeconds(float64(totalSamples) / float64(sampleRate)), true
}

func probeMP3(ctx context.Context, f *os.File, size int64) time.Duration {
	offset := skipID3(f, size)
	var seconds float64
	frames := 0
	for offset+4 <= size {
		if frames&255 == 0 && ctx.Err() != nil {
			return 0
		}
		var h [4]byte
		if !readAt(f, offset, h[:]) {
			break
		}
		rate, frameLength, samples, ok := mp3Frame(h)
		if !ok {
			if frames == 0 {
				return 0
			}
			break
		}
		if frameLength < 4 || offset+int64(frameLength) > size {
			break
		}
		seconds += float64(samples) / float64(rate)
		frames++
		offset += int64(frameLength)
	}
	if frames == 0 {
		return 0
	}
	return durationFromSeconds(seconds)
}

func mp3Frame(h [4]byte) (sampleRate, frameLength, samples int, ok bool) {
	if h[0] != 0xff || h[1]&0xe0 != 0xe0 {
		return 0, 0, 0, false
	}
	version := (h[1] >> 3) & 0x03
	layer := (h[1] >> 1) & 0x03
	bitrateIndex := (h[2] >> 4) & 0x0f
	rateIndex := (h[2] >> 2) & 0x03
	if version == 1 || layer == 0 || bitrateIndex == 0 || bitrateIndex == 15 || rateIndex == 3 {
		return 0, 0, 0, false
	}
	baseRates := [...]int{44100, 48000, 32000}
	rate := baseRates[rateIndex]
	switch version {
	case 2:
		rate /= 2
	case 0:
		rate /= 4
	}
	var bitrateTables [3][16]int
	switch version {
	case 3:
		bitrateTables = [3][16]int{
			{0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448, 0},
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384, 0},
			{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0},
		}
	default:
		bitrateTables = [3][16]int{
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
			{0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256, 0},
			{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0},
		}
	}
	bitrate := bitrateTables[3-layer][bitrateIndex] * 1000
	padding := int((h[2] >> 1) & 1)
	switch layer {
	case layerI:
		frameLength = (12*bitrate/rate + padding) * 4
		samples = 384
	case layerII:
		frameLength = 144*bitrate/rate + padding
		samples = 1152
	case layerIII:
		coefficient := 144
		samples = 1152
		if version != mpeg1 {
			coefficient = 72
			samples = 576
		}
		frameLength = coefficient*bitrate/rate + padding
	}
	return rate, frameLength, samples, rate > 0 && frameLength > 0
}
