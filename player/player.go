package player

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

const (
	sampleRate         = 48000
	channelCount       = 2
	bytesPerSample     = 2
	bytesPerFrame      = channelCount * bytesPerSample
	bytesPerSecond     = sampleRate * channelCount * bytesPerSample
	userAgent          = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	httpReadTimeout    = 15 * time.Second
	remoteRequestBytes = 10 << 20
)

func OutputFormat() string {
	return fmt.Sprintf("System default device, %d kHz, %d channels, %d-bit PCM", sampleRate/1000, channelCount, bytesPerSample*8)
}

var EQFrequencies = [...]float64{31, 62, 125, 250, 500, 1000, 2000, 4000, 8000, 16000}

const EQBandCount = len(EQFrequencies)

var ErrNoFFmpeg = errors.New("player: ffmpeg is not installed or not on PATH")

type Player struct {
	ctl     sync.Mutex
	mu      sync.Mutex
	session *session
	volume  float64

	eqBands   []float64
	speed     float64
	crossfade time.Duration

	next     *session
	advanced string

	lookMu     sync.Mutex
	lookDone   bool
	ffmpegPath string
	lookErr    error
	rangedHTTP bool

	audio *sharedAudio
}

type Option func(*Player)

func WithFFmpeg(path string) Option {
	return func(p *Player) { p.ffmpegPath = path }
}

func withAudio(audio *sharedAudio) Option {
	return func(p *Player) { p.audio = audio }
}

func New(options ...Option) *Player {
	p := &Player{volume: 1, speed: 1, audio: processAudio}
	for _, option := range options {
		option(p)
	}
	go func() { _, _ = p.audio.context() }()
	return p
}

func (p *Player) Play(url string) error {
	return p.PlayContext(context.Background(), url)
}

func (p *Player) PlayContext(ctx context.Context, url string) error {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	return p.start(ctx, url, 0, false)
}

func (p *Player) PlayAt(url string, at time.Duration, paused bool) error {
	return p.PlayAtContext(context.Background(), url, at, paused)
}

func (p *Player) PlayAtContext(ctx context.Context, url string, at time.Duration, paused bool) error {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	return p.start(ctx, url, max(at, 0), paused)
}

func (p *Player) Preload(tag, url string) error {
	return p.PreloadContext(context.Background(), tag, url)
}

func (p *Player) PreloadContext(ctx context.Context, tag, url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("player: stream URL is empty")
	}
	ffmpeg, err := p.resolveFFmpeg(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	loaded := p.session != nil
	p.mu.Unlock()
	if !loaded {
		return nil
	}
	p.ctl.Lock()
	eq, speed := p.eqBands, p.speed
	p.ctl.Unlock()
	next, err := p.decode(ctx, ffmpeg, url, 0, eq, speed)
	if err != nil {
		return err
	}
	next.tag = tag
	p.ctl.Lock()
	next.eq.Set(p.eqBands)
	next.fade.setHold(p.crossfade)
	p.mu.Lock()
	if p.session == nil || p.speed != speed {
		p.mu.Unlock()
		p.ctl.Unlock()
		next.discard()
		return nil
	}
	old := p.next
	p.next = next
	p.mu.Unlock()
	p.ctl.Unlock()
	if old != nil {
		old.discard()
	}
	return nil
}

func (p *Player) Preloaded() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next == nil {
		return ""
	}
	return p.next.tag
}

func (p *Player) CancelPreload() {
	p.mu.Lock()
	next := p.next
	p.next = nil
	p.mu.Unlock()
	if next != nil {
		next.discard()
	}
}

func (p *Player) Advanced() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tag := p.advanced
	p.advanced = ""
	return tag, tag != ""
}

func (p *Player) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && s.paused {
		s.paused = false
		s.out.Play()
	}
}

func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && !s.paused {
		s.paused = true
		s.out.Pause()
	}
}

func (p *Player) Toggle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	if s == nil {
		return
	}
	if s.paused {
		s.paused = false
		s.out.Play()
		return
	}
	s.paused = true
	s.out.Pause()
}

func (p *Player) Seek(at time.Duration) {
	p.SeekContext(context.Background(), at)
}

func (p *Player) SeekContext(ctx context.Context, at time.Duration) {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.mu.Lock()
	s := p.session
	var url string
	var paused bool
	if s != nil {
		url, paused = s.url, s.paused
	}
	p.mu.Unlock()
	if s == nil || at < 0 {
		return
	}
	_ = p.start(ctx, url, at, paused)
}

func (p *Player) Stop() {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.stop()
}

func (p *Player) SetVolume(volume float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = max(volume, 0)
	if p.session != nil {
		p.session.out.SetVolume(p.volume)
	}
}

func (p *Player) SetEQ(gains []float64) {
	if gains != nil && len(gains) != EQBandCount {
		return
	}
	for _, g := range gains {
		if math.IsNaN(g) || math.IsInf(g, 0) {
			return
		}
	}
	p.ctl.Lock()
	defer p.ctl.Unlock()
	if equalGains(p.eqBands, gains) {
		return
	}
	p.eqBands = append([]float64(nil), gains...)
	p.mu.Lock()
	if p.session != nil {
		p.session.eq.Set(p.eqBands)
	}
	if p.next != nil {
		p.next.eq.Set(p.eqBands)
	}
	p.mu.Unlock()
}

const MaxCrossfade = 12 * time.Second

func (p *Player) SetCrossfade(d time.Duration) {
	d = min(max(d, 0), MaxCrossfade)
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.crossfade = d
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != nil {
		p.session.fade.setHold(d)
	}
	if p.next != nil {
		p.next.fade.setHold(d)
	}
}

func (p *Player) Crossfade() time.Duration {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	return p.crossfade
}

func equalGains(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

const (
	MinSpeed = 0.5
	MaxSpeed = 4.0
)

func (p *Player) SetSpeed(ctx context.Context, speed float64) error {
	if math.IsNaN(speed) || speed < MinSpeed || speed > MaxSpeed {
		return nil
	}
	p.ctl.Lock()
	defer p.ctl.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.speed == speed {
		return nil
	}
	p.mu.Lock()
	p.speed = speed
	s := p.session
	var url string
	var at time.Duration
	var paused, ended bool
	if s != nil {
		url, at, paused, ended = s.url, s.played(), s.paused, p.endedLocked(s)
	}
	p.mu.Unlock()
	if s == nil || ended {
		return nil
	}
	return p.start(ctx, url, at, paused)
}

func (p *Player) Speed() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.speed
}

func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

func (p *Player) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil
}

func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	if s == nil || s.paused || p.endedLocked(s) {
		return false
	}
	return s.out == nil || s.out.Err() == nil
}

func (p *Player) Buffering() bool {
	p.mu.Lock()
	s := p.session
	paused := s != nil && s.paused
	p.mu.Unlock()
	hasGivenSound := s != nil && s.source.read.Load() > 0
	if s == nil || paused || hasGivenSound {
		return false
	}
	select {
	case <-s.done:
		return p.pendingSamples(s)
	default:
		return true
	}
}

func (p *Player) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.session.paused
}

func (p *Player) Ended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.endedLocked(p.session)
}

func (p *Player) Position() time.Duration {
	p.mu.Lock()
	s := p.session
	var at time.Duration
	if s != nil {
		at = s.played()
	}
	p.mu.Unlock()
	return at
}

func (s *session) played() time.Duration {
	played := max(s.source.read.Load()-int64(s.out.BufferedSize()), 0)
	return s.offset + time.Duration(float64(played)*s.speed*float64(time.Second)/bytesPerSecond)
}

func (p *Player) Failure() string {
	p.mu.Lock()
	s := p.session
	stopped := s != nil && s.stopped
	p.mu.Unlock()
	if s == nil || stopped {
		return ""
	}
	if err := p.audioFailure(s); err != nil {
		return "audio device: " + err.Error()
	}
	select {
	case <-s.done:
	default:
		return ""
	}
	message := strings.TrimSpace(s.stderr.String())
	p.mu.Lock()
	exitedCleanlyAfterSound := s.waitErr == nil && message == "" && s.source.read.Load() > 0
	p.mu.Unlock()
	if exitedCleanlyAfterSound || p.pendingSamples(s) {
		return ""
	}
	if message == "" {
		message = "ffmpeg stopped"
	}
	return message
}

func (p *Player) StreamFailed() bool {
	p.mu.Lock()
	s := p.session
	stopped := s != nil && s.stopped
	p.mu.Unlock()
	if s == nil || stopped {
		return false
	}
	if err := p.audioFailure(s); err != nil {
		return false
	}
	select {
	case <-s.done:
	default:
		return false
	}
	return strings.TrimSpace(s.stderr.String()) != ""
}

func endOfStream(read int64, eof, devicePlaying, paused bool) bool {
	return read > 0 && eof && !devicePlaying && !paused
}

func (p *Player) endedLocked(s *session) bool {
	return endOfStream(s.source.read.Load(), s.source.eof.Load(), s.out.IsPlaying(), s.paused)
}

func (p *Player) start(ctx context.Context, url string, at time.Duration, paused bool) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("player: stream URL is empty")
	}
	ffmpeg, err := p.resolveFFmpeg(ctx)
	if err != nil {
		return err
	}
	audioCtx, err := p.audio.context()
	if err != nil {
		return err
	}
	p.stop()

	s, err := p.decode(ctx, ffmpeg, url, at, p.eqBands, p.speed)
	if err != nil {
		return err
	}
	s.paused = paused
	s.fade.setHold(p.crossfade)
	s.out = audioCtx.newPlayer(s.source)
	p.mu.Lock()
	s.out.SetVolume(p.volume)
	p.session = s
	p.mu.Unlock()
	if !paused {
		s.out.Play()
	}
	return nil
}

func (p *Player) decode(ctx context.Context, ffmpeg, url string, at time.Duration, eq []float64, speed float64) (*session, error) {
	cmd := exec.CommandContext(context.WithoutCancel(ctx), ffmpeg, streamArgs(url, at, p.rangedHTTP, speed)...)
	hideWindow(cmd)
	pipe, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("player: create audio pipe: %w", err)
	}
	cmd.Stdout = writer
	stderr := &boundedBuffer{limit: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = pipe.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("player: start ffmpeg: %w", err)
	}
	_ = writer.Close()
	s := &session{
		url: url, cmd: cmd, pipe: pipe, stderr: stderr,
		offset: at, speed: speed, done: make(chan struct{}), quit: make(chan struct{}), eq: newEqualizer(eq),
	}
	s.fade = newFadeReader(newEQReader(pipe, s.eq), func() *session { return p.upcoming(s) })
	s.source = newCountingReader(s.fade, func() io.Reader { return p.handoff(s) })
	go p.reap(s)
	return s, nil
}

func streamArgs(url string, at time.Duration, ranged bool, speed float64) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-xerror"}
	if isHTTP(url) {
		args = append(args,
			"-protocol_whitelist", "http,https,httpproxy,tcp,tls,crypto",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
			"-rw_timeout", strconv.FormatInt(httpReadTimeout.Microseconds(), 10),
			"-user_agent", userAgent,
		)
		if ranged {
			args = append(args, "-request_size", strconv.Itoa(remoteRequestBytes))
		}
	}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	args = append(args, "-i", url)
	if speed != 1 {
		args = append(args, "-af", tempoFilter(speed))
	}
	args = append(args,
		"-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", strconv.Itoa(channelCount), "-ar", strconv.Itoa(sampleRate),
	)
	return append(args, "pipe:1")
}

const maxAtempoFactor = 2

func tempoFilter(speed float64) string {
	format := func(f float64) string { return "atempo=" + strconv.FormatFloat(f, 'f', -1, 64) }
	if speed > maxAtempoFactor {
		return format(maxAtempoFactor) + "," + format(speed/maxAtempoFactor)
	}
	return format(speed)
}

func isHTTP(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func (p *Player) reap(s *session) {
	waitErr := s.cmd.Wait()
	p.mu.Lock()
	s.waitErr = waitErr
	p.mu.Unlock()
	close(s.done)
	select {
	case <-s.source.finished:
	case <-s.quit:
	}
	_ = s.pipe.Close()
}

func (p *Player) handoff(from *session) io.Reader {
	select {
	case <-from.done:
	case <-from.quit:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	next := p.upcomingLocked(from)
	if next == nil || from.fade.mixing && from.fade.follower != nil && from.fade.follower != next {
		return nil
	}
	next.out, next.paused = from.out, from.paused
	p.session, p.next, p.advanced = next, nil, next.tag
	return next.source
}

func (p *Player) upcoming(from *session) *session {
	select {
	case <-from.done:
	case <-from.quit:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upcomingLocked(from)
}

func (p *Player) upcomingLocked(from *session) *session {
	next := p.next
	if p.session != from || next == nil || from.waitErr != nil || from.stderr.String() != "" {
		return nil
	}
	select {
	case <-next.done:
		if next.waitErr != nil || next.stderr.String() != "" {
			p.next = nil
			next.discard()
			return nil
		}
	default:
	}
	return next
}

func (p *Player) pendingSamples(s *session) bool {
	p.mu.Lock()
	clean := s.waitErr == nil
	p.mu.Unlock()
	if !clean || s.stderr.String() != "" || s.source.read.Load() > 0 {
		return false
	}
	select {
	case <-s.source.finished:
		return false
	default:
		return true
	}
}

func (p *Player) stop() {
	p.mu.Lock()
	s, next := p.session, p.next
	p.session, p.next, p.advanced = nil, nil, ""
	if s != nil {
		s.stopped = true
	}
	p.mu.Unlock()
	if next != nil {
		next.discard()
	}
	if s == nil {
		return
	}
	close(s.quit)
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pipe.Close()
	s.out.PauseAndStopReading()
}

func (s *session) discard() {
	close(s.quit)
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pipe.Close()
}

func (p *Player) resolveFFmpeg(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.lookMu.Lock()
	defer p.lookMu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if p.lookDone {
		if p.lookErr != nil {
			return "", ErrNoFFmpeg
		}
		return p.ffmpegPath, nil
	}
	path := p.ffmpegPath
	if path == "" {
		var err error
		path, err = exec.LookPath("ffmpeg")
		if err != nil {
			p.lookErr = err
			p.lookDone = true
			return "", ErrNoFFmpeg
		}
	}
	ranged, err := supportsRanges(ctx, path)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p.ffmpegPath = path
	p.rangedHTTP = ranged
	p.lookDone = true
	return p.ffmpegPath, nil
}

func supportsRanges(ctx context.Context, path string) (bool, error) {
	parentCtx := ctx
	ctx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-hide_banner", "-h", "protocol=http")
	hideWindow(cmd)
	help, err := cmd.Output()
	if err := parentCtx.Err(); err != nil {
		return false, err
	}
	return err == nil && bytes.Contains(help, []byte("-request_size")), nil
}

type output interface {
	Play()
	Pause()
	SetVolume(float64)
	BufferedSize() int
	IsPlaying() bool
	PauseAndStopReading()
	Err() error
}

type device interface {
	newPlayer(io.Reader) output
	Err() error
}

type otoDevice struct{ ctx *oto.Context }

func (d otoDevice) newPlayer(r io.Reader) output { return d.ctx.NewPlayer(r) }
func (d otoDevice) Err() error                   { return d.ctx.Err() }

type sharedAudio struct {
	open func() (device, error)
	once sync.Once
	dev  device
	err  error
}

var processAudio = &sharedAudio{open: openOto}

func openOto() (device, error) {
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate:      sampleRate,
		ChannelCount:    channelCount,
		Format:          oto.FormatSignedInt16LE,
		ApplicationName: "Meiro",
	})
	if err != nil {
		return nil, err
	}
	<-ready
	return otoDevice{ctx}, nil
}

func (a *sharedAudio) context() (device, error) {
	a.once.Do(func() {
		dev, err := a.open()
		if err != nil {
			a.err = fmt.Errorf("player: open the audio device: %w", err)
			return
		}
		a.dev = dev
	})
	return a.dev, a.err
}

func (p *Player) audioFailure(s *session) error {
	if s == nil || s.out == nil {
		return nil
	}
	if err := s.out.Err(); err != nil {
		return err
	}
	if dev, err := p.audio.context(); err == nil && dev != nil {
		return dev.Err()
	}
	return nil
}

type session struct {
	url    string
	tag    string
	cmd    *exec.Cmd
	pipe   io.ReadCloser
	stderr *boundedBuffer
	source *countingReader
	eq     *equalizer
	fade   *fadeReader
	out    output
	done   chan struct{}
	quit   chan struct{}

	offset  time.Duration
	speed   float64
	paused  bool
	stopped bool
	waitErr error
}

type countingReader struct {
	reader   io.Reader
	read     atomic.Int64
	eof      atomic.Bool
	finished chan struct{}
	once     sync.Once
	follow   func() io.Reader
	onward   io.Reader
}

func newCountingReader(reader io.Reader, follow func() io.Reader) *countingReader {
	return &countingReader{reader: reader, finished: make(chan struct{}), follow: follow}
}

func (c *countingReader) Read(p []byte) (int, error) {
	if c.onward != nil {
		return c.onward.Read(p)
	}
	n, err := c.reader.Read(p)
	c.read.Add(int64(n))
	if err != nil {
		if errors.Is(err, io.EOF) {
			c.eof.Store(true)
		}
		c.once.Do(func() { close(c.finished) })
		if n == 0 && errors.Is(err, io.EOF) && c.follow != nil {
			if onward := c.follow(); onward != nil {
				c.onward = onward
				return c.onward.Read(p)
			}
		}
	}
	return n, err
}

type boundedBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.limit - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
