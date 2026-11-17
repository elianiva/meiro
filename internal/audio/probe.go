package audio

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"time"
)

func Probe(ctx context.Context, path string) time.Duration {
	if ctx.Err() != nil {
		return 0
	}
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.Size() < 4 {
		return 0
	}
	return probeFile(ctx, f, info.Size())
}

func probeFile(ctx context.Context, f *os.File, size int64) time.Duration {
	header := make([]byte, min(size, 12))
	if !readAt(f, 0, header) {
		return 0
	}
	switch {
	case len(header) >= 12 && (string(header[:4]) == "RIFF" || string(header[:4]) == "RF64") && string(header[8:12]) == "WAVE":
		return probeWAV(f, size)
	case string(header[:4]) == "fLaC":
		return probeFLAC(f)
	case string(header[:4]) == "OggS":
		return probeOgg(ctx, f, size)
	case len(header) >= 4 && string(header[:4]) == "\x1aE\xdf\xa3":
		return probeWebM(f, size)
	}
	if d := probeMP4(f, size); d > 0 {
		return d
	}
	if d, ok := probeAAC(ctx, f, size); ok {
		return d
	}
	return probeMP3(ctx, f, size)
}

func readAt(f *os.File, offset int64, data []byte) bool {
	if offset < 0 || len(data) == 0 {
		return len(data) == 0
	}
	n, err := f.ReadAt(data, offset)
	return n == len(data) && (err == nil || err == io.EOF)
}

func durationFromSeconds(seconds float64) time.Duration {
	if seconds <= 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0
	}
	return time.Duration(seconds * float64(time.Second))
}

func probeWAV(f *os.File, size int64) time.Duration {
	var byteRate, dataSize, rf64DataSize uint64
	for offset := int64(12); offset+8 <= size; {
		var chunk [8]byte
		if !readAt(f, offset, chunk[:]) {
			return 0
		}
		length := uint64(binary.LittleEndian.Uint32(chunk[4:]))
		start := offset + 8
		if string(chunk[:4]) == "data" && length == uint64(^uint32(0)) {
			length = rf64DataSize
		}
		if length > uint64(size-start) {
			return 0
		}
		switch string(chunk[:4]) {
		case "ds64":
			var ds64 [16]byte
			if length < uint64(len(ds64)) || !readAt(f, start, ds64[:]) {
				return 0
			}
			rf64DataSize = binary.LittleEndian.Uint64(ds64[8:16])
		case "fmt ":
			var format [16]byte
			if length < uint64(len(format)) || !readAt(f, start, format[:]) {
				return 0
			}
			byteRate = uint64(binary.LittleEndian.Uint32(format[8:12]))
		case "data":
			dataSize += length
		}
		next := start + int64(length)
		if length&1 != 0 {
			next++
		}
		if next <= offset {
			return 0
		}
		offset = next
	}
	if byteRate == 0 || dataSize == 0 {
		return 0
	}
	return durationFromSeconds(float64(dataSize) / float64(byteRate))
}

func probeFLAC(f *os.File) time.Duration {
	var header [4]byte
	if !readAt(f, 4, header[:]) || header[0]&0x7f != 0 || uint32(header[1])<<16|uint32(header[2])<<8|uint32(header[3]) < 34 {
		return 0
	}
	var streamInfo [34]byte
	if !readAt(f, 8, streamInfo[:]) {
		return 0
	}
	packed := binary.BigEndian.Uint64(streamInfo[10:18])
	sampleRate := packed >> 44
	totalSamples := packed & ((uint64(1) << 36) - 1)
	if sampleRate == 0 || totalSamples == 0 {
		return 0
	}
	return durationFromSeconds(float64(totalSamples) / float64(sampleRate))
}

func probeMP4(f *os.File, size int64) time.Duration {
	for offset := int64(0); offset+8 <= size; {
		boxSize, headerSize, kind, ok := mp4Box(f, offset, size)
		if !ok {
			return 0
		}
		if kind == "moov" {
			return probeMP4Movie(f, offset+headerSize, offset+boxSize)
		}
		offset += boxSize
	}
	return 0
}

func mp4Box(f *os.File, offset, limit int64) (int64, int64, string, bool) {
	var header [16]byte
	if offset < 0 || offset+8 > limit || !readAt(f, offset, header[:8]) {
		return 0, 0, "", false
	}
	boxSize := int64(binary.BigEndian.Uint32(header[:4]))
	headerSize := int64(8)
	switch boxSize {
	case 1:
		if offset+16 > limit || !readAt(f, offset+8, header[8:16]) {
			return 0, 0, "", false
		}
		u := binary.BigEndian.Uint64(header[8:16])
		if u > math.MaxInt64 {
			return 0, 0, "", false
		}
		boxSize, headerSize = int64(u), 16
	case 0:
		boxSize = limit - offset
	}
	if boxSize < headerSize || boxSize > limit-offset {
		return 0, 0, "", false
	}
	return boxSize, headerSize, string(header[4:8]), true
}

func probeMP4Movie(f *os.File, offset, end int64) time.Duration {
	for offset+8 <= end {
		boxSize, headerSize, kind, ok := mp4Box(f, offset, end)
		if !ok {
			return 0
		}
		if kind == "mvhd" {
			return probeMP4Header(f, offset+headerSize, boxSize-headerSize)
		}
		offset += boxSize
	}
	return 0
}

func probeMP4Header(f *os.File, offset, size int64) time.Duration {
	var header [32]byte
	if size < 20 || !readAt(f, offset, header[:min(size, int64(len(header)))]) {
		return 0
	}
	switch header[0] {
	case 0:
		timescale := binary.BigEndian.Uint32(header[12:16])
		duration := binary.BigEndian.Uint32(header[16:20])
		if timescale == 0 || duration == 0 || duration == math.MaxUint32 {
			return 0
		}
		return durationFromSeconds(float64(duration) / float64(timescale))
	case 1:
		if size < 32 {
			return 0
		}
		timescale := binary.BigEndian.Uint32(header[20:24])
		duration := binary.BigEndian.Uint64(header[24:32])
		if timescale == 0 || duration == 0 || duration == math.MaxUint64 {
			return 0
		}
		return durationFromSeconds(float64(duration) / float64(timescale))
	default:
		return 0
	}
}

const (
	ebmlSegmentID       = 0x18538067
	ebmlInfoID          = 0x1549a966
	ebmlTimecodeScaleID = 0x2ad7b1
	ebmlDurationID      = 0x4489
)

func probeWebM(f *os.File, size int64) time.Duration {
	segmentStart, segmentEnd := int64(0), size
	for offset := int64(0); offset < size; {
		id, idLen, _, ok := ebmlVint(f, offset, true)
		if !ok {
			return 0
		}
		length, sizeLen, unknown, ok := ebmlVint(f, offset+int64(idLen), false)
		if !ok {
			return 0
		}
		dataStart := offset + int64(idLen+sizeLen)
		if id == ebmlSegmentID {
			segmentStart = dataStart
			if !unknown {
				if length > uint64(size-dataStart) {
					return 0
				}
				segmentEnd = dataStart + int64(length)
			}
			break
		}
		if unknown || length > uint64(size-dataStart) {
			return 0
		}
		offset = dataStart + int64(length)
	}

	for offset := segmentStart; offset < segmentEnd; {
		id, idLen, _, ok := ebmlVint(f, offset, true)
		if !ok {
			return 0
		}
		length, sizeLen, unknown, ok := ebmlVint(f, offset+int64(idLen), false)
		if !ok {
			return 0
		}
		dataStart := offset + int64(idLen+sizeLen)
		if dataStart > segmentEnd || unknown || length > uint64(segmentEnd-dataStart) {
			return 0
		}
		dataEnd := dataStart + int64(length)
		if id == ebmlInfoID {
			return probeWebMInfo(f, dataStart, dataEnd)
		}
		offset = dataEnd
	}
	return 0
}

func probeWebMInfo(f *os.File, offset, end int64) time.Duration {
	timecodeScale := uint64(1_000_000)
	var ticks float64
	for offset < end {
		id, idLen, _, ok := ebmlVint(f, offset, true)
		if !ok {
			return 0
		}
		length, sizeLen, unknown, ok := ebmlVint(f, offset+int64(idLen), false)
		dataStart := offset + int64(idLen+sizeLen)
		if !ok || dataStart > end || unknown || length > uint64(end-dataStart) {
			return 0
		}
		switch id {
		case ebmlTimecodeScaleID:
			if length == 0 || length > 8 {
				return 0
			}
			var b [8]byte
			if !readAt(f, dataStart, b[8-int(length):]) {
				return 0
			}
			timecodeScale = binary.BigEndian.Uint64(b[:])
		case ebmlDurationID:
			switch length {
			case 4:
				var b [4]byte
				if !readAt(f, dataStart, b[:]) {
					return 0
				}
				ticks = float64(math.Float32frombits(binary.BigEndian.Uint32(b[:])))
			case 8:
				var b [8]byte
				if !readAt(f, dataStart, b[:]) {
					return 0
				}
				ticks = math.Float64frombits(binary.BigEndian.Uint64(b[:]))
			default:
				return 0
			}
		}
		offset = dataStart + int64(length)
	}
	if ticks <= 0 || timecodeScale == 0 {
		return 0
	}
	return durationFromSeconds(ticks * float64(timecodeScale) / float64(time.Second))
}

func ebmlVint(f *os.File, offset int64, identifier bool) (uint64, int, bool, bool) {
	var first [1]byte
	if !readAt(f, offset, first[:]) || first[0] == 0 {
		return 0, 0, false, false
	}
	mask := byte(0x80)
	length := 1
	for first[0]&mask == 0 {
		mask >>= 1
		length++
	}
	if length > 8 {
		return 0, 0, false, false
	}
	var b [8]byte
	if !readAt(f, offset, b[:length]) {
		return 0, 0, false, false
	}
	value := uint64(b[0])
	if !identifier {
		value &= uint64(mask - 1)
	}
	for i := 1; i < length; i++ {
		value = value<<8 | uint64(b[i])
	}
	unknown := !identifier && value == (uint64(1)<<(7*length))-1
	return value, length, unknown, true
}

func probeOgg(ctx context.Context, f *os.File, size int64) time.Duration {
	var firstPacket []byte
	var granule uint64
	var haveGranule bool
	packetDone := false
	for offset := int64(0); offset+27 <= size; {
		if ctx.Err() != nil {
			return 0
		}
		var header [27]byte
		if !readAt(f, offset, header[:]) || string(header[:4]) != "OggS" || header[4] != 0 {
			return 0
		}
		segments := int(header[26])
		lacing := make([]byte, segments)
		if !readAt(f, offset+27, lacing) {
			return 0
		}
		payloadSize := 0
		for _, n := range lacing {
			payloadSize += int(n)
		}
		pagePayloadStart := offset + 27 + int64(segments)
		if pagePayloadStart+int64(payloadSize) > size {
			return 0
		}
		g := binary.LittleEndian.Uint64(header[6:14])
		if g != math.MaxUint64 {
			granule, haveGranule = g, true
		}
		if !packetDone {
			packetOffset := pagePayloadStart
			for _, n := range lacing {
				if len(firstPacket) < 64 {
					chunk := min(int(n), 64-len(firstPacket))
					part := make([]byte, chunk)
					if !readAt(f, packetOffset, part) {
						return 0
					}
					firstPacket = append(firstPacket, part...)
				}
				packetOffset += int64(n)
				if n < 255 {
					packetDone = true
					break
				}
			}
		}
		offset = pagePayloadStart + int64(payloadSize)
	}
	if !packetDone || !haveGranule {
		return 0
	}
	switch {
	case len(firstPacket) >= 19 && string(firstPacket[:8]) == "OpusHead":
		preSkip := uint64(binary.LittleEndian.Uint16(firstPacket[10:12]))
		if granule <= preSkip {
			return 0
		}
		return durationFromSeconds(float64(granule-preSkip) / 48000)
	case len(firstPacket) >= 16 && string(firstPacket[:7]) == "\x01vorbis":
		sampleRate := binary.LittleEndian.Uint32(firstPacket[12:16])
		if sampleRate == 0 || granule == 0 {
			return 0
		}
		return durationFromSeconds(float64(granule) / float64(sampleRate))
	default:
		return 0
	}
}
