// Package player plays one audio stream at a time.
//
// It asks ffmpeg, in a child process, to decode a stream into signed 16-bit
// stereo PCM, and hands those samples to oto, which writes them to the
// system's audio device. Decoding in a child process keeps the app free of
// cgo and of codecs of its own, and lets it play anything ffmpeg reads.
//
// The zero value is not usable; call New. All methods are safe for
// concurrent use, so the user interface can drive the player from the main
// thread while oto reads samples in the background.
package player

import (
	"errors"
	"fmt"
	"io"
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
	// sampleRate, channelCount and bytesPerSample are the PCM format the
	// player asks ffmpeg for and gives oto. 48 kHz stereo is what most
	// YouTube Music audio decodes from.
	sampleRate     = 48000
	channelCount   = 2
	bytesPerSample = 2
	// bytesPerSecond is the size of one second of that PCM stream.
	bytesPerSecond = sampleRate * channelCount * bytesPerSample
	// userAgent is what ffmpeg sends when it opens the stream. YouTube's
	// media servers answer some requests without one with an error.
	userAgent       = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"
	httpReadTimeout = 15 * time.Second
)

// ErrNoFFmpeg reports that ffmpeg is not on PATH, which the player needs to
// decode a stream.
var ErrNoFFmpeg = errors.New("player: ffmpeg is not installed or not on PATH")

// Player plays one stream at a time.
type Player struct {
	// ctl serialises the calls that replace or end the stream: Play, Seek and
	// Stop. They do slow work (killing and starting ffmpeg, waiting on the
	// audio device), so they run under ctl alone and take mu only to swap the
	// session in or out. The readers the UI asks every frame take mu, and so
	// never wait on that work.
	ctl     sync.Mutex
	mu      sync.Mutex
	session *session
	volume  float64
	// next is the stream decoding ahead of the end of the current one, and
	// advanced is the tag of the one that took over without a pause, until
	// Advanced reports it.
	next     *session
	advanced string

	lookOnce   sync.Once
	ffmpegPath string
	lookErr    error

	audio *sharedAudio
}

// Option configures a Player.
type Option func(*Player)

// WithFFmpeg makes the player decode with the ffmpeg at path, instead of the
// one on PATH.
func WithFFmpeg(path string) Option {
	return func(p *Player) { p.ffmpegPath = path }
}

// withAudio supplies the audio context used by a Player. It is kept internal
// so tests can replace the operating-system output without exposing it as an
// application configuration surface.
func withAudio(audio *sharedAudio) Option {
	return func(p *Player) { p.audio = audio }
}

// New creates a player that is not yet playing anything. It does not touch
// the audio device or the file system.
func New(options ...Option) *Player {
	p := &Player{volume: 1, audio: processAudio}
	for _, option := range options {
		option(p)
	}
	// Opening the device can take a long while; do it now, off the thread
	// that will later press play.
	go func() { _, _ = p.audio.context() }()
	return p
}

// Available reports whether the player can play at all: it needs ffmpeg.
func (p *Player) Available() bool {
	_, err := p.resolveFFmpeg()
	return err == nil
}

// Play starts url from its beginning, replacing anything playing. It returns
// once ffmpeg has started, not when the stream ends.
func (p *Player) Play(url string) error {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	return p.start(url, 0, false)
}

// PlayAt is Play from the position at, and held when paused. It is how a
// stream that broke off is picked up again where it stopped.
func (p *Player) PlayAt(url string, at time.Duration, paused bool) error {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	return p.start(url, max(at, 0), paused)
}

// Preload starts decoding url in the background, to follow the stream playing
// the moment that one ends, without the pause of opening it then. tag names
// what it is, as Advanced reports it. It replaces an earlier Preload, and a
// Play, PlayAt, Seek or Stop drops it, as the track it was for may no longer
// be the one that follows. Nothing is preloaded while no stream is loaded.
//
// Only a stream that ends cleanly hands over: one that failed, or whose
// preloaded successor failed to open, ends as it would have, and what follows
// is for the caller to decide.
func (p *Player) Preload(tag, url string) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("player: stream URL is empty")
	}
	ffmpeg, err := p.resolveFFmpeg()
	if err != nil {
		return err
	}
	p.mu.Lock()
	loaded := p.session != nil
	p.mu.Unlock()
	if !loaded {
		return nil
	}
	next, err := p.decode(ffmpeg, url, 0)
	if err != nil {
		return err
	}
	next.tag = tag
	p.mu.Lock()
	if p.session == nil {
		p.mu.Unlock()
		next.discard()
		return nil
	}
	old := p.next
	p.next = next
	p.mu.Unlock()
	if old != nil {
		old.discard()
	}
	return nil
}

// Preloaded returns the tag of the stream waiting to follow the current one,
// or an empty string when there is none.
func (p *Player) Preloaded() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.next == nil {
		return ""
	}
	return p.next.tag
}

// CancelPreload drops the preloaded stream, if any.
func (p *Player) CancelPreload() {
	p.mu.Lock()
	next := p.next
	p.next = nil
	p.mu.Unlock()
	if next != nil {
		next.discard()
	}
}

// Advanced returns the tag of the preloaded stream that has taken over from
// the one before it, once, and false when no handover has happened since it
// was last asked.
func (p *Player) Advanced() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	tag := p.advanced
	p.advanced = ""
	return tag, tag != ""
}

// Resume starts a paused stream again, and does nothing when it plays or has
// ended.
func (p *Player) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && s.paused {
		s.paused = false
		s.out.Play()
	}
}

// Pause holds the stream where it is, keeping a little of it decoded so a
// resume does not stutter.
func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s := p.session; s != nil && !s.paused {
		s.paused = true
		s.out.Pause()
	}
}

// Toggle pauses a playing stream and resumes a paused one.
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

// Seek moves to at within the stream by restarting the decode there. It
// keeps the stream paused when it was paused.
func (p *Player) Seek(at time.Duration) {
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
	_ = p.start(url, at, paused)
}

// Stop ends the stream and forgets it.
func (p *Player) Stop() {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.stop()
}

// SetVolume sets the playback gain, 1 for the stream's own level. Values
// below 0 are 0 and above 1 may clip.
func (p *Player) SetVolume(volume float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.volume = max(volume, 0)
	if p.session != nil {
		p.session.out.SetVolume(p.volume)
	}
}

// Volume returns the current gain, 1 by default.
func (p *Player) Volume() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

// Active reports whether a stream is loaded, playing, paused or ended.
func (p *Player) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil
}

// Playing reports whether the stream is playing now.
func (p *Player) Playing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.session
	if s == nil || s.paused || p.endedLocked(s) {
		return false
	}
	// The lock is held; Err on oto objects does not take it.
	return s.out == nil || s.out.Err() == nil
}

// Buffering reports whether the stream is playing but has not yet given any
// sound: ffmpeg is still opening it, or catching up after a seek.
func (p *Player) Buffering() bool {
	p.mu.Lock()
	s := p.session
	paused := s != nil && s.paused
	p.mu.Unlock()
	if s == nil || paused || s.source.read.Load() > 0 {
		return false
	}
	select {
	case <-s.done:
		// Stopped without a sample: Failure says why, unless the reader has
		// yet to take what ffmpeg wrote before it exited.
		return p.pendingSamples(s)
	default:
		return true
	}
}

// Paused reports whether the stream is loaded and held.
func (p *Player) Paused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.session.paused
}

// Ended reports whether the stream has been played to its end.
func (p *Player) Ended() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.session != nil && p.endedLocked(p.session)
}

// Position returns how far playback has come, which leads where the user
// hears by the audio device's buffer, a few tens of milliseconds.
func (p *Player) Position() time.Duration {
	p.mu.Lock()
	s := p.session
	p.mu.Unlock()
	if s == nil {
		return 0
	}
	played := max(s.source.read.Load()-int64(s.out.BufferedSize()), 0)
	return s.offset + time.Duration(played*int64(time.Second)/bytesPerSecond)
}

// Failure returns what ffmpeg reported when the decode stopped because of an
// error, as an unreachable stream, and an empty string when the stream ended
// or the app stopped it.
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
	// ffmpeg decodes faster than the track plays, so it exits cleanly a little
	// before the end. A clean exit has no errors on stderr; -xerror makes
	// decode and read failures nonzero, and the stderr check also preserves an
	// error from older ffmpeg builds that exit successfully after one.
	p.mu.Lock()
	finished := s.waitErr == nil && message == "" && s.source.read.Load() > 0
	p.mu.Unlock()
	if finished || p.pendingSamples(s) {
		return ""
	}
	if message == "" {
		message = "ffmpeg stopped"
	}
	return message
}

// StreamFailed reports whether the last decode stopped because the stream
// could not be read, as a connection YouTube's server reset, rather than
// because the audio device stopped working or the stream ended.
func (p *Player) StreamFailed() bool {
	p.mu.Lock()
	s := p.session
	stopped := s != nil && s.stopped
	p.mu.Unlock()
	if s == nil || stopped {
		return false
	}
	if err := p.audioFailure(s); err != nil {
		return false // the sound, not the stream, is what stopped
	}
	select {
	case <-s.done:
	default:
		return false // the decode is still running
	}
	return strings.TrimSpace(s.stderr.String()) != ""
}

// endOfStream says whether a stream has been played to its end. A paused
// stream also finds its device player not playing while the samples it holds
// drain or sit, which is not the track's end: only an unpaused stream with
// the device idle and its data read out says that.
func endOfStream(read int64, eof, devicePlaying, paused bool) bool {
	return read > 0 && eof && !devicePlaying && !paused
}

func (p *Player) endedLocked(s *session) bool {
	return endOfStream(s.source.read.Load(), s.source.eof.Load(), s.out.IsPlaying(), s.paused)
}

// start replaces the current stream with url, decoded from at. The caller
// holds ctl and not mu.
func (p *Player) start(url string, at time.Duration, paused bool) error {
	if strings.TrimSpace(url) == "" {
		return errors.New("player: stream URL is empty")
	}
	ffmpeg, err := p.resolveFFmpeg()
	if err != nil {
		return err
	}
	audioCtx, err := p.audio.context()
	if err != nil {
		return err
	}
	p.stop()

	s, err := p.decode(ffmpeg, url, at)
	if err != nil {
		return err
	}
	s.paused = paused
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

// decode starts ffmpeg on url from at, and the goroutine that reaps it. The
// session has no output yet: start gives it one, and a handoff takes over the
// one of the stream before it.
func (p *Player) decode(ffmpeg, url string, at time.Duration) (*session, error) {
	cmd := exec.Command(ffmpeg, streamArgs(url, at)...) //nolint:noctx // the player ends ffmpeg itself when the stream is replaced or stopped
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
		offset: at, done: make(chan struct{}), quit: make(chan struct{}),
	}
	s.source = newCountingReader(pipe, func() io.Reader { return p.handoff(s) })
	go p.reap(s)
	return s, nil
}

// streamArgs is the ffmpeg command line that decodes url from at into signed
// 16-bit stereo PCM on stdout.
func streamArgs(url string, at time.Duration) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-xerror"}
	if isHTTP(url) {
		// A remote stream, or a manifest it names, cannot reach local files.
		args = append(args,
			"-protocol_whitelist", "http,https,httpproxy,tcp,tls,crypto",
			"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
			"-rw_timeout", strconv.FormatInt(httpReadTimeout.Microseconds(), 10),
			"-user_agent", userAgent,
		)
	}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at.Seconds(), 'f', 3, 64))
	}
	return append(args,
		"-i", url,
		"-vn", "-f", "s16le", "-acodec", "pcm_s16le",
		"-ac", strconv.Itoa(channelCount), "-ar", strconv.Itoa(sampleRate),
		"pipe:1",
	)
}

// isHTTP reports whether rawURL is read over HTTP, the only protocol that
// defines the reconnect and user-agent options streamArgs adds. A cached
// track is a plain path, and ffmpeg refuses an input given an option its
// protocol does not define, so it must not receive them. Add a scheme here to
// give it the same options.
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

// reap observes process completion independently from the audio reader, so a
// paused session cannot prevent an ffmpeg exit from being noticed. The pipe is
// ours rather than Wait's, so the samples still in it survive the exit; reap
// closes it once the reader has taken them all, or the app has stopped.
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

// handoff is what the output reads on when the stream from ends: the preloaded
// stream, now the current one, in the same output, so that the sound goes on
// with no restart of it. It returns nil when there is nothing to hand over to,
// or from did not end cleanly.
func (p *Player) handoff(from *session) io.Reader {
	select {
	case <-from.done:
	case <-from.quit:
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
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
	next.out, next.paused = from.out, from.paused
	p.session, p.next, p.advanced = next, nil, next.tag
	return next.source
}

// pendingSamples reports whether ffmpeg exited cleanly without the reader
// having taken anything yet, so its output may still sit in the pipe.
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

// stop ends the current stream, if any. The caller holds ctl and not mu: the
// session is detached under mu, and the slow teardown runs outside it.
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
	// ffmpeg goes first, and the pipe with it. oto may be blocked reading a
	// stream that has stalled, and PauseAndStopReading waits for that read, on
	// the thread of whoever pressed stop.
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pipe.Close()
	s.out.PauseAndStopReading()
}

// discard ends a decode that never reached the output.
func (s *session) discard() {
	close(s.quit)
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pipe.Close()
}

func (p *Player) resolveFFmpeg() (string, error) {
	p.lookOnce.Do(func() {
		if p.ffmpegPath == "" {
			p.ffmpegPath, p.lookErr = exec.LookPath("ffmpeg")
		}
	})
	if p.lookErr != nil {
		return "", ErrNoFFmpeg
	}
	return p.ffmpegPath, nil
}

// output is the part of an oto player a session drives.
type output interface {
	Play()
	Pause()
	SetVolume(float64)
	BufferedSize() int
	IsPlaying() bool
	PauseAndStopReading()
	Err() error
}

// device is the process-wide audio context.
type device interface {
	newPlayer(io.Reader) output
	Err() error
}

type otoDevice struct{ ctx *oto.Context }

func (d otoDevice) newPlayer(r io.Reader) output { return d.ctx.NewPlayer(r) }
func (d otoDevice) Err() error                   { return d.ctx.Err() }

// sharedAudio opens an audio device once and remembers the outcome. Oto allows
// one context per process and cannot retry a failed open, so a failure is
// kept rather than hidden behind a second attempt that would only report
// "context is already created".
type sharedAudio struct {
	open func() (device, error)
	once sync.Once
	dev  device
	err  error
}

// processAudio is the context every Player shares.
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

// context opens the audio device on first use, and reports why it could not.
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

// audioFailure returns why the device or the session's output stopped working,
// or nil. The caller must not hold p.mu.
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

// session is one decode: a child ffmpeg, the pipe it writes PCM to, and the
// oto player reading that pipe.
type session struct {
	url string
	// tag names a preloaded stream to the caller that asked for it.
	tag    string
	cmd    *exec.Cmd
	pipe   io.ReadCloser
	stderr *boundedBuffer
	source *countingReader
	out    output
	done   chan struct{}
	// quit is closed when the app stops the decode.
	quit chan struct{}

	// offset is where in the track this decode began.
	offset time.Duration
	paused bool
	// stopped is set when the app, not ffmpeg, ended the decode.
	stopped bool
	waitErr error
}

// countingReader counts the bytes oto has taken from the pipe, and remembers
// the end of the stream.
type countingReader struct {
	reader io.Reader
	read   atomic.Int64
	eof    atomic.Bool
	// finished is closed when a read fails or reaches the end.
	finished chan struct{}
	once     sync.Once
	// follow is asked for the stream to carry on with when this one ends,
	// and nil means none. Once one is given, reads go on to it.
	follow func() io.Reader
	onward io.Reader
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

// boundedBuffer keeps the first limit bytes written to it, which is enough
// of ffmpeg's error output to explain a failed decode.
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
