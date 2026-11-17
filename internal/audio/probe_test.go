package audio

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProbeDuration(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want time.Duration
	}{
		{name: "wav", data: wavFixture(), want: 2 * time.Second},
		{name: "flac", data: flacFixture(), want: 2 * time.Second},
		{name: "mp4", data: mp4Fixture(), want: 2500 * time.Millisecond},
		{name: "webm", data: webmFixture(), want: 2500 * time.Millisecond},
		{name: "ogg opus", data: oggOpusFixture(), want: 2 * time.Second},
		{name: "aac", data: aacFixture(), want: 86 * 1024 * time.Second / 44100},
		{name: "mp3 with id3", data: mp3Fixture(), want: 77 * 1152 * time.Second / 44100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "audio")
			if err := os.WriteFile(path, tt.data, 0o600); err != nil {
				t.Fatal(err)
			}
			got := Probe(context.Background(), path)
			if delta := got - tt.want; delta < -25*time.Millisecond || delta > 25*time.Millisecond {
				t.Errorf("Probe() = %v, want %v (±25ms)", got, tt.want)
			}
		})
	}
}

func TestProbeDurationRejectsUnsupportedAndCanceled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio")
	if err := os.WriteFile(path, []byte("not an audio file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Probe(context.Background(), path); got != 0 {
		t.Errorf("unsupported file duration = %v, want 0", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Probe(ctx, path); got != 0 {
		t.Errorf("canceled probe duration = %v, want 0", got)
	}
}

func wavFixture() []byte {
	const dataBytes = 32000
	b := make([]byte, 44+dataBytes)
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], 8000)
	binary.LittleEndian.PutUint32(b[28:], 16000)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], dataBytes)
	return b
}

func flacFixture() []byte {
	b := make([]byte, 4+4+34)
	copy(b, "fLaC")
	b[4] = 0x80
	b[7] = 34
	packed := uint64(44100)<<44 | 88200
	binary.BigEndian.PutUint64(b[18:26], packed)
	return b
}

func mp4Fixture() []byte {
	mvhd := make([]byte, 20)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)
	binary.BigEndian.PutUint32(mvhd[16:], 2500)
	return append(mp4BoxFixture("ftyp", []byte("M4A \x00\x00\x00\x00M4A ")),
		mp4BoxFixture("moov", mp4BoxFixture("mvhd", mvhd))...)
}

func mp4BoxFixture(kind string, body []byte) []byte {
	b := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	copy(b[4:], kind)
	copy(b[8:], body)
	return b
}

func webmFixture() []byte {
	timecodeScale := []byte{0x2a, 0xd7, 0xb1, 0x83, 0x0f, 0x42, 0x40}
	duration := make([]byte, 11)
	copy(duration, []byte{0x44, 0x89, 0x88})
	binary.BigEndian.PutUint64(duration[3:], math.Float64bits(2500))
	info := make([]byte, 0, len(timecodeScale)+len(duration))
	info = append(info, timecodeScale...)
	info = append(info, duration...)
	infoElement := append([]byte{0x15, 0x49, 0xa9, 0x66, byte(0x80 | len(info))}, info...)
	return append([]byte{0x1a, 0x45, 0xdf, 0xa3, 0x80, 0x18, 0x53, 0x80, 0x67, 0xff}, infoElement...)
}

func oggOpusFixture() []byte {
	head := make([]byte, 19)
	copy(head, "OpusHead")
	binary.LittleEndian.PutUint16(head[10:], 312)
	first := oggPageFixture(0, 0, head)
	second := oggPageFixture(0x04, 312+2*48000, []byte{0})
	return append(first, second...)
}

func oggPageFixture(flags byte, granule uint64, payload []byte) []byte {
	b := make([]byte, 28+len(payload))
	copy(b, "OggS")
	b[5] = flags
	binary.LittleEndian.PutUint64(b[6:], granule)
	b[26], b[27] = 1, byte(len(payload))
	copy(b[28:], payload)
	return b
}

func aacFixture() []byte {
	const frameCount = 86
	b := make([]byte, frameCount*7)
	for i := range frameCount {
		h := b[i*7 : (i+1)*7]
		h[0], h[1], h[2], h[5] = 0xff, 0xf1, 4<<2, 7<<5
	}
	return b
}

func mp3Fixture() []byte {
	const frameLength, frameCount = 417, 77
	b := make([]byte, 10+frameLength*frameCount)
	copy(b, "ID3\x04\x00\x00\x00\x00\x00\x00")
	for i := range frameCount {
		frame := b[10+i*frameLength : 10+(i+1)*frameLength]
		copy(frame, []byte{0xff, 0xfb, 0x90, 0x64})
	}
	return b
}
