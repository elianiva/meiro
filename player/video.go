package player

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// defaultVideoFPS is the rate a picture whose own rate is unknown
	// decodes at.
	defaultVideoFPS = 30
	// videoAhead is how many frames are decoded before they are due. The
	// decoder waits when that many wait, so a stream that nobody takes
	// frames from, as one in a hidden player, decodes nothing.
	videoAhead = 2
	// videoLead is how far ahead of the sound a picture that replaces
	// another starts decoding, so its first frame is ready before it is
	// due: the one it replaces shows until then. A 1080p YouTube stream
	// takes about 2.3 s from the start of ffmpeg to its first frame, and
	// starting the GPU about 0.6 s more.
	videoLead = 4 * time.Second
)

// videoSession is one ffmpeg decoding a stream's picture into RGBA frames,
// that Frame hands out in step with the sound.
type videoSession struct {
	url string
	// height is the height every frame is decoded at, and the width 16:9 of
	// it: the stream's frames are scaled to fit and padded with black, so a
	// frame's size is known before the first one arrives, as raw video
	// carries no size of its own.
	height int
	// fps is the rate frames come at: the stream's own, which ffmpeg holds
	// it to, so a frame's time is its place in the stream alone.
	fps float64
	// gpu says the picture decodes on the GPU; got, that a frame has come,
	// and drained, closed when the reader stops, that none will come.
	gpu bool
	got atomic.Bool
	// latest is the time of the newest frame decoded, in nanoseconds, or
	// just before the offset until one is: a decode yet to give a frame
	// has not reached even where it began.
	latest  atomic.Int64
	drained chan struct{}
	cmd     *exec.Cmd
	pipe    *os.File
	stderr  *boundedBuffer
	offset  time.Duration
	quit    chan struct{} // closed when the session is stopped
	done    chan struct{} // closed once ffmpeg has exited
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
// with it from where it is now, decoded at height and at fps, the stream's
// frame rate, or defaultVideoFPS when that is 0. It does nothing when no
// stream is loaded, or when that picture is already shown or on its way. A
// picture that replaces another starts a little ahead of the sound, and the
// one it replaces shows until its first frame is due, so the switch shows
// no gap; if it fails before then, the one shown stays. A Seek keeps the
// picture, moving it with the sound; a new stream and Stop drop it. Once
// ctx is done it does nothing and returns ctx's error, so a request that
// was superseded cannot replace the picture of the one that superseded it.
func (p *Player) SetVideo(ctx context.Context, url string, height int, fps float64) error {
	if fps <= 0 {
		fps = defaultVideoFPS
	}
	p.ctl.Lock()
	defer p.ctl.Unlock()
	// A request superseded while it waited for ctl must not undo the one
	// that superseded it, which may have gone first.
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	loaded := p.session != nil
	p.mu.Unlock()
	if !loaded {
		return nil
	}
	ffmpeg, err := p.resolveFFmpeg(ctx)
	if err != nil {
		return err
	}
	gpu := p.videoGPU(ctx, ffmpeg)
	p.mu.Lock()
	s, shown, next := p.session, p.video, p.nextVideo
	var at time.Duration
	if s != nil {
		at = s.played()
		if !s.paused && shown != nil {
			at += videoLead
		}
	}
	p.mu.Unlock()
	if s == nil {
		return nil
	}
	if next.is(url, height) {
		return nil
	}
	if shown.is(url, height) {
		// Back to the picture shown before the one on its way came.
		p.mu.Lock()
		p.nextVideo = nil
		p.mu.Unlock()
		next.end()
		return nil
	}
	if shown == nil {
		p.stopVideo()
	} else {
		p.mu.Lock()
		p.nextVideo = nil
		p.mu.Unlock()
		next.end()
	}
	v, err := p.decodeVideo(ffmpeg, url, height, fps, gpu, at)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session != s {
		// A gapless handoff swapped the stream while the picture was
		// starting: this one belongs to the track that ended. The app asks
		// again for the track playing.
		v.end()
		return nil
	}
	if shown == nil {
		p.video = v
	} else {
		p.nextVideo = v
	}
	return nil
}

// StopVideo drops the picture, leaving the sound playing.
func (p *Player) StopVideo() {
	p.ctl.Lock()
	defer p.ctl.Unlock()
	p.stopVideo()
}

// VideoURL returns the URL of the picture shown, or an empty string when
// there is none. A picture on its way to replace it is not shown yet.
func (p *Player) VideoURL() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.video == nil {
		return ""
	}
	return p.video.url
}

// VideoFPS returns the frame rate of the picture shown, or 0 when there is
// none.
func (p *Player) VideoFPS() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.video == nil {
		return 0
	}
	return p.video.fps
}

// Frame returns the picture due at the stream's position: the newest frame
// at or before it, and whether that is a different frame from the one
// returned last. The caller may draw the picture until the next call that
// reports a change, when it is decoded into again. It is nil while no frame
// has come yet, and when no picture is shown. When the first frame of a
// picture on its way is due, that picture takes the place of the one shown.
// A picture that failed on the GPU before its first frame decodes again on
// the CPU.
func (p *Player) Frame() (*image.RGBA, bool) {
	p.mu.Lock()
	failed := p.video.failedOnGPU() || p.nextVideo.failedOnGPU()
	p.mu.Unlock()
	if failed {
		p.fallBackToCPU()
	}
	p.mu.Lock()
	v, next, s := p.video, p.nextVideo, p.session
	var at time.Duration
	if s != nil {
		at = s.played()
	}
	var ended *videoSession
	if next != nil {
		switch {
		case next.failureLocked() != "" && !next.failedOnGPU():
			// The picture shown stays.
			p.nextVideo, ended = nil, next
		case next.due(at):
			p.video, p.nextVideo, ended, v = next, nil, v, next
		}
	}
	p.mu.Unlock()
	ended.end()
	if v == nil {
		return nil, false
	}
	return v.frame(at)
}

// VideoLag returns how far the picture shown has fallen behind the sound: the
// sound's position less the time of the newest frame decoded. Until a frame
// has come it is positive, even at once after a seek. It is negative while
// the decoder is ahead, as it keeps itself, and 0 when no picture is shown
// or its decode has stopped, at its end or on a failure, as no frame is
// then on its way. While the sound is paused the decoder catches up to it,
// so a caller that holds the sound until the lag is no longer positive lets
// a stalled picture catch up, and waits for a new one's first frame.
func (p *Player) VideoLag() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, s := p.video, p.session
	if v == nil || s == nil {
		return 0
	}
	select {
	case <-v.drained:
		return 0
	default:
	}
	return s.played() - time.Duration(v.latest.Load())
}

// VideoStarting reports whether the picture shown has yet to give a frame
// since its decode began, as at once after a seek, and still may.
func (p *Player) VideoStarting() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	v := p.video
	if v == nil || v.got.Load() {
		return false
	}
	select {
	case <-v.drained:
		return false
	default:
		return true
	}
}

// VideoFailure returns what ffmpeg reported when the picture stopped because
// of an error, and an empty string while it decodes, when it ended, or when
// the app stopped it. A picture that fails on the GPU reports nothing until
// Frame has tried it on the CPU, and failed there too.
func (p *Player) VideoFailure() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.video == nil || p.video.failedOnGPU() { // it decodes again on the CPU
		return ""
	}
	return p.video.failureLocked()
}

// failureLocked is VideoFailure for one session. The caller holds the
// player's mu.
func (v *videoSession) failureLocked() string {
	if v.stopped {
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

// is reports whether v decodes url at height; nil decodes nothing.
func (v *videoSession) is(url string, height int) bool {
	return v != nil && v.url == url && v.height == height
}

// due reports whether the first frame not yet shown has come and is due at
// position at.
func (v *videoSession) due(at time.Duration) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.pending.img == nil {
		select {
		case v.pending = <-v.frames:
		default:
			return false
		}
	}
	return v.pending.at <= at
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

// decodeVideo starts ffmpeg on the picture of url from at, at height and fps,
// on the GPU when gpu is set, the goroutine that
// reads its frames and the one that reaps it. The caller holds ctl.
func (p *Player) decodeVideo(ffmpeg, url string, height int, fps float64, gpu bool, at time.Duration) (*videoSession, error) {
	cmd := exec.Command(ffmpeg, videoArgs(url, height, fps, gpu, at, p.rangedHTTP)...) //nolint:noctx // the player ends ffmpeg itself when the picture is replaced or stopped
	hideWindow(cmd)
	pipe, writer, err := newVideoPipe()
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
		url: url, height: height, fps: fps, gpu: gpu, drained: make(chan struct{}), cmd: cmd, pipe: pipe, stderr: stderr, offset: at,
		quit: make(chan struct{}), done: make(chan struct{}),
		frames: make(chan videoFrame, videoAhead), free: make(chan *image.RGBA, buffers),
	}
	for range buffers {
		v.free <- image.NewRGBA(image.Rect(0, 0, videoWidth(height), height))
	}
	v.latest.Store(int64(at) - 1)
	go v.read()
	go p.reapVideo(v)
	return v, nil
}

// read decodes frames from the pipe into free buffers until the stream ends
// or the session is stopped. A frame's time is its count from the start of
// the decode: ffmpeg gives them at the session's fps, evenly spaced.
func (v *videoSession) read() {
	defer close(v.drained)
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
		v.got.Store(true)
		frame := videoFrame{img: buf, at: v.offset + time.Duration(float64(i)*float64(time.Second)/v.fps)}
		v.latest.Store(int64(frame.at))
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
	shown, next := p.dropVideoLocked()
	p.mu.Unlock()
	shown.end()
	next.end()
}

// dropVideoLocked detaches the picture shown and the one on its way from the
// player, for the caller to end. The caller holds mu.
func (p *Player) dropVideoLocked() (shown, next *videoSession) {
	shown, next = p.video, p.nextVideo
	p.video, p.nextVideo = nil, nil
	if shown != nil {
		shown.stopped = true
	}
	return shown, next
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
// at into RGBA frames of height, and 16:9 of that wide, at fps on stdout.
// With gpu, the GPU decodes the stream where it can decode its codec, and
// ffmpeg decodes it where not, and libplacebo scales the frames and turns
// them into RGBA on the GPU. Without it the CPU scales the frames and turns
// them into RGBA; on Windows Direct3D 11 decodes them where the GPU can, and
// the CPU where not.
func videoArgs(url string, height int, fps float64, gpu bool, at time.Duration, ranged bool) []string {
	width := strconv.Itoa(videoWidth(height))
	rate := ",fps=" + strconv.FormatFloat(fps, 'f', -1, 64)
	args := inputArgs(url, at, ranged)
	// A frame the GPU decoded comes back as NV12, which swscale turns into
	// RGBA far slower than it turns NV12 into YUV 4:2:0 and that into RGBA,
	// so it goes that way; an RGB picture stays RGB. One filter thread does
	// the work in less CPU time than several.
	filter := "format=yuv420p|gbrp,scale=" + width + ":" + strconv.Itoa(height) + ":force_original_aspect_ratio=decrease," +
		"pad=" + width + ":" + strconv.Itoa(height) + ":(ow-iw)/2:(oh-ih)/2" + rate
	switch {
	case gpu:
		args = append(append(gpuArgs(), "-hwaccel", "vulkan", "-hwaccel_output_format", "vulkan"), args...)
		filter = gpuFilter(height) + rate
	case runtime.GOOS == "windows":
		args = append([]string{"-hwaccel", "d3d11va"}, args...)
	}
	return append(args,
		"-an", "-sn", "-dn",
		"-filter_threads", "1",
		"-vf", filter,
		"-f", "rawvideo", "-pix_fmt", "rgba",
		"pipe:1",
	)
}

// gpuFilter scales frames to height, and 16:9 of that wide, padded with
// black, and turns them into RGBA, with libplacebo on the GPU. Padding by
// normalize_sar, rather than fit_mode, works with ffmpeg before 8.
func gpuFilter(height int) string {
	return "libplacebo=w=" + strconv.Itoa(videoWidth(height)) + ":h=" + strconv.Itoa(height) +
		":format=rgba:normalize_sar=1,hwdownload,format=rgba"
}

// gpuArgs open a Vulkan device for ffmpeg's filters.
func gpuArgs() []string {
	return []string{"-init_hw_device", "vulkan=vk", "-filter_hw_device", "vk"}
}

// videoGPU reports whether pictures decode on the GPU. It asks ffmpeg once
// whether it can: the GPU path needs Vulkan, a GPU that has it, and an
// ffmpeg built with libplacebo. The caller holds ctl.
func (p *Player) videoGPU(ctx context.Context, ffmpeg string) bool {
	if !p.gpuChecked {
		ok := probeGPU(ctx, ffmpeg)
		if ctx.Err() != nil {
			return false // ask again next time
		}
		p.gpuChecked, p.gpuVideo = true, ok
	}
	return p.gpuVideo
}

// probeGPU reports whether the ffmpeg at path turns a frame into RGBA on the
// GPU, by sending one small frame through the filter a picture goes
// through. An ffmpeg or a machine without Vulkan or libplacebo, or with a
// libplacebo that lacks an option the filter names, fails it, and decodes
// on the CPU.
func probeGPU(ctx context.Context, path string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := append(gpuArgs(), "-hide_banner", "-loglevel", "error", "-nostdin",
		"-f", "lavfi", "-i", "color=c=black:s=64x36:d=1",
		"-vf", "hwupload,"+gpuFilter(36),
		"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "pipe:1")
	cmd := exec.CommandContext(ctx, path, args...)
	hideWindow(cmd)
	out, err := cmd.Output()
	return err == nil && len(out) == 64*36*4
}

// failedOnGPU reports whether v decoded on the GPU and stopped before its
// first frame, as on a driver that opens Vulkan but cannot decode the
// stream; nil did not. The caller holds the player's mu.
func (v *videoSession) failedOnGPU() bool {
	if v == nil || !v.gpu || v.stopped || v.got.Load() {
		return false
	}
	select {
	case <-v.drained:
		return true
	default:
		return false
	}
}

// fallBackToCPU decodes again on the CPU, from where the sound is, each
// picture that failed on the GPU before its first frame, and every later
// picture there too. While ctl is busy it does nothing, and the next Frame
// asks again: Frame runs on the main thread, which must not wait.
func (p *Player) fallBackToCPU() {
	if !p.ctl.TryLock() {
		return
	}
	defer p.ctl.Unlock()
	p.gpuVideo = false
	ffmpeg, err := p.resolveFFmpeg(context.Background())
	if err != nil {
		return
	}
	for _, slot := range []**videoSession{&p.video, &p.nextVideo} {
		p.mu.Lock()
		v := *slot
		failed := v.failedOnGPU()
		var at time.Duration
		if p.session != nil {
			at = p.session.played()
		}
		p.mu.Unlock()
		if !failed {
			continue
		}
		w, err := p.decodeVideo(ffmpeg, v.url, v.height, v.fps, false, at)
		p.mu.Lock()
		if *slot != v {
			p.mu.Unlock()
			w.end()
			continue
		}
		// A picture that cannot start again is gone, and the app asks for
		// it anew.
		*slot = nil
		if err == nil {
			*slot = w
		}
		v.stopped = true
		p.mu.Unlock()
		v.end()
	}
}

// videoWidth is the width of a 16:9 frame height tall.
func videoWidth(height int) int { return height * 16 / 9 }
