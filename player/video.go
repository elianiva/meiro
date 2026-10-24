package player

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

const (
	// videoWidth and videoHeight are the canvas every frame is decoded onto,
	// scaled to fit and padded with black, so a frame's size is known before
	// the first one arrives: raw video carries no size of its own. 720p is
	// as much as the player's picture shows at any window size.
	videoWidth  = 1280
	videoHeight = 720
	// VideoFPS is the rate frames come at, whatever the stream's own, so a
	// frame's time is its place in the stream alone.
	VideoFPS = 30
	// videoAhead is how many frames are decoded before they are due. The
	// decoder waits when that many wait, so a stream that nobody takes
	// frames from, as one in a hidden player, decodes nothing.
	videoAhead = 2
)

// videoSession is one ffmpeg decoding a stream's picture into RGBA frames,
// that Frame hands out in step with the sound.
type videoSession struct {
	url    string
	cmd    *exec.Cmd
	pipe   *os.File
	stderr *boundedBuffer
	offset time.Duration
	quit   chan struct{} // closed when the session is stopped
	done   chan struct{} // closed once ffmpeg has exited
	// waitErr is what Wait returned, under the player's mu once done is
	// closed; stopped says the player ended it, so its exit is no failure.
	waitErr error
	stopped bool

	// frames are the decoded frames not yet due, oldest first; free are the
	// buffers the reader decodes into. Every buffer is in one of frames,
	// free, pending or shown, or under the reader's pen, so returning a
	// buffer to free never waits. The reader does wait on frames when it is
	// ahead, which is why its sends also listen on quit.
	frames chan videoFrame
	free   chan *image.RGBA

	// mu guards what Frame keeps between calls: the frame taken from frames
	// but not yet due, and the one in the caller's hands.
	mu      sync.Mutex
	pending videoFrame
	shown   *image.RGBA
}

// videoFrame is a decoded picture and when in the stream it shows.
type videoFrame struct {
	img *image.RGBA
	at  time.Duration
}

// SetVideo shows the picture of url, the stream playing's video, in step
// with it from where it is now. It replaces the picture shown, and does
// nothing when no stream is loaded. A Seek keeps the picture, moving it with
// the sound; a new stream and Stop drop it. Once ctx is done it does
// nothing and returns ctx's error, so a request that was superseded cannot
// replace the picture of the one that superseded it.
func (p *Player) SetVideo(ctx context.Context, url string) error {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.mu.Lock()
	s := p.session
	var at time.Duration
	if s != nil {
		at = s.played()
	}
	p.mu.Unlock()
	if s == nil {
		return nil
	}
	ffmpeg, err := p.resolveFFmpeg(ctx)
	if err != nil {
		return err
	}
	p.stopVideo()
	p.mu.Lock()
	// Until the new picture decodes, a Seek has none to carry over.
	s.video = ""
	p.mu.Unlock()
	v, err := p.decodeVideo(ffmpeg, url, at)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if p.session != s {
		// A gapless handoff swapped the stream while the picture was
		// starting: this one belongs to the track that ended. The app asks
		// again for the track playing.
		p.mu.Unlock()
		v.end()
		return nil
	}
	s.video = url
	p.video = v
	p.mu.Unlock()
	return nil
}

// StopVideo drops the picture, leaving the sound playing.
func (p *Player) StopVideo() {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.mu.Lock()
	if p.session != nil {
		p.session.video = ""
	}
	p.mu.Unlock()
	p.stopVideo()
}

// VideoURL returns the URL of the picture shown, or an empty string when
// there is none.
func (p *Player) VideoURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.video == nil {
		return ""
	}
	return p.video.url
}

// Frame returns the picture due at the stream's position: the newest frame
// at or before it, and whether that is a different frame from the one
// returned last. The caller may draw the picture until the next call that
// reports a change, when it is decoded into again. It is nil while no frame
// has come yet, and when no picture is shown.
func (p *Player) Frame() (*image.RGBA, bool) {
	p.mu.Lock()
	v, s := p.video, p.session
	var at time.Duration
	if s != nil {
		at = s.played()
	}
	p.mu.Unlock()
	if v == nil {
		return nil, false
	}
	return v.frame(at)
}

// VideoFailure returns what ffmpeg reported when the picture stopped because
// of an error, and an empty string while it decodes, when it ended, or when
// the app stopped it.
func (p *Player) VideoFailure() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.video
	if v == nil || v.stopped {
		return ""
	}
	select {
	case <-v.done:
	default:
		return ""
	}
	if message := v.stderr.String(); message != "" {
		return message
	}
	if v.waitErr != nil {
		return v.waitErr.Error()
	}
	return ""
}

// frame is Frame for one session, at position at.
func (v *videoSession) frame(at time.Duration) (*image.RGBA, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	changed := false
	for {
		if v.pending.img == nil {
			select {
			case v.pending = <-v.frames:
			default:
				return v.shown, changed
			}
		}
		if v.pending.at > at {
			return v.shown, changed
		}
		// Due, or late: it takes the place of the one shown, which goes
		// back to the decoder.
		if v.shown != nil {
			v.free <- v.shown
		}
		v.shown, v.pending, changed = v.pending.img, videoFrame{}, true
	}
}

// decodeVideo starts ffmpeg on the picture of url from at, the goroutine that
// reads its frames and the one that reaps it. The caller holds ctl.
func (p *Player) decodeVideo(ffmpeg, url string, at time.Duration) (*videoSession, error) {
	cmd := exec.Command(ffmpeg, videoArgs(url, at, p.rangedHTTP)...) //nolint:noctx // the player ends ffmpeg itself when the picture is replaced or stopped
	hideWindow(cmd)
	pipe, writer, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("player: create video pipe: %w", err)
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
	// One buffer each for the frame shown, the one waiting its turn, the
	// decoder's, and videoAhead between.
	buffers := videoAhead + 3
	v := &videoSession{
		url: url, cmd: cmd, pipe: pipe, stderr: stderr, offset: at,
		quit: make(chan struct{}), done: make(chan struct{}),
		frames: make(chan videoFrame, videoAhead), free: make(chan *image.RGBA, buffers),
	}
	for range buffers {
		v.free <- image.NewRGBA(image.Rect(0, 0, videoWidth, videoHeight))
	}
	go v.read()
	go p.reapVideo(v)
	return v, nil
}

// read decodes frames from the pipe into free buffers until the stream ends
// or the session is stopped. A frame's time is its count from the start of
// the decode: ffmpeg gives them at VideoFPS whatever the stream's own rate.
func (v *videoSession) read() {
	defer func() { _ = v.pipe.Close() }()
	for i := int64(0); ; i++ {
		var buf *image.RGBA
		select {
		case buf = <-v.free:
		case <-v.quit:
			return
		}
		if _, err := io.ReadFull(v.pipe, buf.Pix); err != nil {
			return
		}
		frame := videoFrame{img: buf, at: v.offset + time.Duration(i)*time.Second/VideoFPS}
		select {
		case v.frames <- frame:
		case <-v.quit:
			return
		}
	}
}

// reapVideo records how ffmpeg exited. The pipe is the reader's: what is
// still in it after the exit is read out as frames.
func (p *Player) reapVideo(v *videoSession) {
	waitErr := v.cmd.Wait()
	p.mu.Lock()
	v.waitErr = waitErr
	p.mu.Unlock()
	close(v.done)
}

// stopVideo ends the picture, if any. The caller holds ctl and not mu.
func (p *Player) stopVideo() {
	p.mu.Lock()
	v := p.dropVideoLocked()
	p.mu.Unlock()
	v.end()
}

// dropVideoLocked detaches the picture from the player, for the caller to
// end. The caller holds mu.
func (p *Player) dropVideoLocked() *videoSession {
	v := p.video
	p.video = nil
	if v != nil {
		v.stopped = true
	}
	return v
}

// end kills the decode of a detached picture; nil is nothing to end.
func (v *videoSession) end() {
	if v == nil {
		return
	}
	close(v.quit)
	if v.cmd.Process != nil {
		_ = v.cmd.Process.Kill()
	}
	_ = v.pipe.Close()
}

// videoArgs is the ffmpeg command line that decodes the picture of url from
// at into RGBA frames of videoWidth by videoHeight at VideoFPS on stdout.
func videoArgs(url string, at time.Duration, ranged bool) []string {
	size := strconv.Itoa(videoWidth) + ":" + strconv.Itoa(videoHeight)
	args := inputArgs(url, at, ranged)
	return append(args,
		"-an", "-sn", "-dn",
		"-vf", "scale="+size+":force_original_aspect_ratio=decrease,pad="+size+":(ow-iw)/2:(oh-ih)/2,fps="+strconv.Itoa(VideoFPS),
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"pipe:1",
	)
}
