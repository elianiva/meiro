package main

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	neturl "net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// thumbBudget is how many bytes of pixels the cache keeps before dropping the
// bitmaps that went longest without being drawn. MyGo holds a bitmap on the
// GPU as long as the app uses it, so the cache must not grow without end
// while the user browses. It counts bytes, not bitmaps, because a cover is
// tens of kilobytes at one size and over a megabyte at another.
const thumbBudget = 128 << 20

// thumbSourceBudget bounds compressed pictures kept for rebuilding evicted
// bitmaps without downloading them again.
const thumbSourceBudget = 32 << 20

// thumbRetry is how long a failed download first waits before the next try.
// Each further failure doubles the wait, up to thumbRetryMax.
const thumbRetry = 10 * time.Second

// thumbRetryMax caps the wait between attempts at a picture that keeps
// failing for a reason that may pass, such as a dropped connection.
const thumbRetryMax = 5 * time.Minute

// thumbFailures bounds the failure record, so that pages of broken covers
// cannot grow it without end.
const thumbFailures = 256

// thumbQueueLimit bounds how many downloads wait at once. A page asks for
// dozens of covers, and a fast scroll asks for more before the first have
// landed; the least recently wanted wait is dropped and only asked for again
// if it returns to view.
const thumbQueueLimit = 128

// thumbKeyMemo bounds the remembered (URL, size) derivations. The working set
// is the covers on screen, so falling back to a fresh map costs one short
// burst of regex work.
const thumbKeyMemo = 4096

// thumbCache keeps decoded artwork and a smaller cache of compressed sources.
type thumbCache struct {
	client *http.Client
	notify func()
	// synth, when set, answers every request without the network: tests give
	// pages artwork of their own making.
	synth func(url string, size int) *ui.Bitmap

	mu sync.Mutex
	// cond wakes the workers that download and decode at most thumbFetches
	// pictures at a time.
	cond    *sync.Cond
	queue   map[string]thumbJob
	bitmaps map[string]*thumb
	// recent orders bitmaps from most to least recently drawn, so eviction
	// takes the back instead of scanning every entry.
	recent  *list.List
	sources map[string]*thumbSource
	// held and sourceHeld count their respective caches' bytes. tick orders
	// bitmap touches and source-cache reads and writes for both LRUs. seq
	// orders waits in the queue, so the most recently wanted picture is
	// served first.
	held       int
	sourceHeld int
	tick       uint64
	seq        uint64
	pending    map[string]bool
	// keys remembers the sized URL and the sizeless base for a (URL, size)
	// pair, so repeated lookups skip the regex work.
	keys map[thumbRequest]thumbKeys
	// failed holds how each download or decode failed. A permanent failure,
	// such as a 404 or a picture that will not decode, is never retried; a
	// transient one backs off further with each attempt.
	failed map[string]thumbFailure
	// sizes remembers which sizes of each picture are in bitmaps, by the
	// picture's URL without its size, so a small copy can stand in while a
	// larger one downloads.
	sizes map[string][]string
}

// thumbRequest is the (URL, size) pair a lookup asks about.
type thumbRequest struct {
	url  string
	size int
}

// thumbKeys is the derivation of a thumbRequest: the URL asked for at that
// size, and its sizeless base that groups every size of one picture.
type thumbKeys struct {
	sized string
	base  string
}

// thumbJob is a picture waiting for a worker. A source means its compressed
// bytes are already at hand, so no download is needed.
type thumbJob struct {
	tick   uint64
	source []byte
}

// thumbFailure is how a failed picture waits for its next attempt.
type thumbFailure struct {
	at        time.Time
	attempts  int
	permanent bool
}

// wait is how long a failure waits before the next attempt: the base wait
// doubled once per failure so far, never beyond the cap.
func (f thumbFailure) wait() time.Duration {
	wait := thumbRetry
	for i := 1; i < f.attempts && wait < thumbRetryMax; i++ {
		wait *= 2
	}
	return min(wait, thumbRetryMax)
}

// errTooLarge and errDecode mark a picture that will never load, however many
// times it is asked for.
var (
	errTooLarge = errors.New("picture is larger than the limit")
	errDecode   = errors.New("picture could not be decoded")
)

// statusError is a non-2xx answer from the picture host.
type statusError struct{ code int }

func (e statusError) Error() string { return "HTTP " + strconv.Itoa(e.code) }

// permanentFailure reports whether asking for the same URL again could ever
// succeed. A 4xx other than a timeout or a rate limit will not, and neither
// will a picture that is too large or fails to decode.
func permanentFailure(err error) bool {
	var status statusError
	if errors.As(err, &status) {
		switch status.code {
		case http.StatusRequestTimeout, http.StatusTooManyRequests:
			return false
		}
		return status.code >= 400 && status.code < 500
	}
	return errors.Is(err, errTooLarge) || errors.Is(err, errDecode)
}

// thumb is one cached bitmap, the colour taken from it, and what it costs.
type thumb struct {
	bitmap *ui.Bitmap
	// url and element place the bitmap in thumbCache.recent.
	url     string
	element *list.Element
	// colour is only taken from the picture when something asks for it, so
	// covers never pay for a theme seed they will not use. colourReady
	// separates "not taken yet" from "taken and found nothing".
	colour      ui.Color
	hasColour   bool
	colourReady bool
	bytes       int
	used        uint64
}

// thumbSource is the compressed picture kept after its bitmap is evicted.
type thumbSource struct {
	data []byte
	used uint64
}

// thumbBytes estimates the memory a bitmap takes: its pixels, and the
// smaller copies MyGo makes of them for drawing it small, which add up to a
// third more.
func thumbBytes(b *ui.Bitmap) int {
	w, h := b.Size()
	return w * h * 4 * 4 / 3
}

// newThumbCache returns a cache that calls notify when a bitmap lands, so
// the window draws it.
func newThumbCache(notify func()) *thumbCache {
	t := &thumbCache{
		client:  &http.Client{Timeout: 30 * time.Second},
		notify:  notify,
		bitmaps: make(map[string]*thumb),
		recent:  list.New(),
		sources: make(map[string]*thumbSource),
		queue:   make(map[string]thumbJob),
		pending: make(map[string]bool),
		failed:  make(map[string]thumbFailure),
		keys:    make(map[thumbRequest]thumbKeys),
		sizes:   make(map[string][]string),
	}
	t.cond = sync.NewCond(&t.mu)
	for range thumbFetches {
		go t.worker()
	}
	return t
}

// bitmap returns the artwork at url, asked for at size pixels across. While
// it downloads or rebuilds from a compressed source, it returns another size
// of the same picture when one is loaded, and nil otherwise.
func (t *thumbCache) bitmap(url string, size int) *ui.Bitmap {
	return t.bitmapIf(url, size, true)
}

// bitmapIf returns a picture only when it may be fetched. Carousel cards
// outside the visible range use this to avoid touching cache entries or
// starting work for clipped children.
func (t *thumbCache) bitmapIf(url string, size int, fetch bool) *ui.Bitmap {
	if url == "" || !fetch {
		return nil
	}
	if t.synth != nil {
		return t.synth(url, size)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := t.keyLocked(url, size)
	if entry, ok := t.bitmaps[key.sized]; ok {
		return t.touch(entry)
	}
	if t.pending[key.sized] {
		// The picture is already on its way. Asking for it again means it
		// is still visible, which moves it ahead of stale waits.
		if job, queued := t.queue[key.sized]; queued {
			t.seq++
			job.tick = t.seq
			t.queue[key.sized] = job
		}
	} else if source, ok := t.source(key.sized); ok {
		t.pending[key.sized] = true
		t.enqueueLocked(key.sized, source)
	} else if t.retryDue(key.sized) {
		t.pending[key.sized] = true
		t.enqueueLocked(key.sized, nil)
	}
	for _, other := range t.sizes[key.base] {
		if entry, ok := t.bitmaps[other]; ok {
			return t.touch(entry)
		}
	}
	return nil
}

// keyLocked derives the sized URL and sizeless base of a request, remembering
// the answer so repeated lookups skip the regex work. The caller holds the
// lock.
func (t *thumbCache) keyLocked(url string, size int) thumbKeys {
	request := thumbRequest{url: url, size: size}
	if key, ok := t.keys[request]; ok {
		return key
	}
	sized := thumbnailURL(url, size)
	key := thumbKeys{sized: sized, base: sizeless(sized)}
	if len(t.keys) >= thumbKeyMemo {
		t.keys = make(map[thumbRequest]thumbKeys)
	}
	t.keys[request] = key
	return key
}

// retryDue reports whether a missing picture may be asked for again. A
// permanent failure never may; a transient one waits out its backoff. The
// caller holds the lock.
func (t *thumbCache) retryDue(url string) bool {
	failure, ok := t.failed[url]
	if !ok {
		return true
	}
	if failure.permanent {
		return false
	}
	return time.Since(failure.at) >= failure.wait()
}

// touch marks a bitmap as drawn now and returns it.
func (t *thumbCache) touch(entry *thumb) *ui.Bitmap {
	t.tick++
	entry.used = t.tick
	if entry.element != nil {
		t.recent.MoveToFront(entry.element)
	}
	return entry.bitmap
}

// source returns compressed bytes from memory and marks them recently used.
// The returned bytes are immutable and remain valid if the entry is evicted.
// The caller holds t.mu.
func (t *thumbCache) source(url string) ([]byte, bool) {
	entry, ok := t.sources[url]
	if !ok {
		return nil, false
	}
	t.tick++
	entry.used = t.tick
	return entry.data, true
}

// thumbFetches limits concurrent downloads and bitmap rebuilds.
const thumbFetches = 6

// thumbLimit is the largest picture the cache will take.
const thumbLimit = 8 << 20

// worker downloads and decodes one queued picture at a time. thumbFetches
// workers run together, which bounds the cache's concurrent work.
func (t *thumbCache) worker() {
	for {
		t.mu.Lock()
		for len(t.queue) == 0 {
			t.cond.Wait()
		}
		url, job := t.popLocked()
		t.mu.Unlock()
		if job.source != nil {
			t.decode(url, job.source, false)
			continue
		}
		data, err := t.download(url)
		if err != nil {
			t.failedFetch(url, err)
			continue
		}
		t.decode(url, data, true)
	}
}

// enqueueLocked adds a picture to the work queue, dropping the least recently
// wanted wait when the queue is full. The caller holds the lock.
func (t *thumbCache) enqueueLocked(url string, source []byte) {
	if _, queued := t.queue[url]; !queued && len(t.queue) >= thumbQueueLimit {
		t.dropStaleLocked()
	}
	t.seq++
	t.queue[url] = thumbJob{tick: t.seq, source: source}
	t.pending[url] = true
	t.cond.Broadcast()
}

// popLocked takes the most recently wanted job, so visible pictures are not
// starved by stale waits queued behind them. The caller holds the lock.
func (t *thumbCache) popLocked() (string, thumbJob) {
	url, found := "", false
	for key, job := range t.queue {
		if !found || job.tick > t.queue[url].tick {
			url, found = key, true
		}
	}
	job := t.queue[url]
	delete(t.queue, url)
	return url, job
}

// dropStaleLocked forgets the least recently wanted wait and lets a later
// frame ask for it again only if it is still wanted. The caller holds the
// lock.
func (t *thumbCache) dropStaleLocked() {
	stale, found := "", false
	for url, job := range t.queue {
		if !found || job.tick < t.queue[stale].tick {
			stale, found = url, true
		}
	}
	if !found {
		return
	}
	delete(t.queue, stale)
	delete(t.pending, stale)
}

// download fetches the picture at url.
func (t *thumbCache) download(url string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, statusError{code: response.StatusCode}
	}
	// One byte more than the limit tells a picture that fits from one cut off.
	size := 512 << 10
	if n := response.ContentLength; n > thumbLimit {
		return nil, errTooLarge
	} else if n > 0 {
		size = int(n)
	}
	buffer := bytes.NewBuffer(make([]byte, 0, size+1))
	_, err = io.Copy(buffer, io.LimitReader(response.Body, thumbLimit+1))
	data := buffer.Bytes()
	if err != nil {
		return nil, err
	}
	if len(data) > thumbLimit {
		return nil, errTooLarge
	}
	return data, nil
}

// decode creates a bitmap, retaining downloaded source bytes. The colour is
// taken later, when something asks for it. Package ui registers the JPEG, PNG,
// GIF, WebP and BMP decoders with image.Decode, and this package imports ui,
// so the artwork formats YouTube serves all decode here.
func (t *thumbCache) decode(url string, data []byte, keepSource bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.failedFetch(url, fmt.Errorf("%w: %v", errDecode, err))
		return
	}
	bitmap := ui.NewBitmap(img)
	t.mu.Lock()
	delete(t.pending, url)
	delete(t.failed, url)
	if keepSource {
		t.storeSource(url, data)
	}
	t.store(url, &thumb{bitmap: bitmap, bytes: thumbBytes(bitmap)})
	t.mu.Unlock()
	t.notify()
}

// failedFetch notes a failed download or decode and schedules a redraw for
// the next attempt. A permanent failure is never retried, and a transient one
// waits out an ever longer backoff.
func (t *thumbCache) failedFetch(url string, err error) {
	t.mu.Lock()
	delete(t.pending, url)
	failure := t.failed[url]
	failure.attempts++
	failure.at = time.Now()
	failure.permanent = failure.permanent || permanentFailure(err)
	t.failed[url] = failure
	t.pruneFailedLocked()
	wait, permanent := failure.wait(), failure.permanent
	t.mu.Unlock()
	if permanent {
		return
	}
	// Draw again once the wait is over, so the next frame tries again.
	time.AfterFunc(wait+time.Second, t.notify)
}

// pruneFailedLocked keeps the failure record small: it forgets failures whose
// retry wait has passed, then the oldest ones. The caller holds the lock.
func (t *thumbCache) pruneFailedLocked() {
	if len(t.failed) <= thumbFailures {
		return
	}
	now := time.Now()
	for url, failure := range t.failed {
		if !failure.permanent && now.Sub(failure.at) >= failure.wait() {
			delete(t.failed, url)
		}
	}
	for len(t.failed) > thumbFailures {
		oldest, found := "", false
		for url, failure := range t.failed {
			if !found || failure.at.Before(t.failed[oldest].at) {
				oldest, found = url, true
			}
		}
		if !found {
			break
		}
		delete(t.failed, oldest)
	}
}

// storeSource keeps compressed bytes under their own LRU budget. The caller
// holds the lock.
func (t *thumbCache) storeSource(url string, data []byte) {
	if len(data) > thumbSourceBudget {
		return
	}
	if old, ok := t.sources[url]; ok {
		t.sourceHeld -= len(old.data)
	}
	t.tick++
	t.sources[url] = &thumbSource{data: data, used: t.tick}
	t.sourceHeld += len(data)
	for t.sourceHeld > thumbSourceBudget {
		oldest, found := "", false
		for key, entry := range t.sources {
			if key != url && (!found || entry.used < t.sources[oldest].used) {
				oldest, found = key, true
			}
		}
		if !found {
			break
		}
		t.sourceHeld -= len(t.sources[oldest].data)
		delete(t.sources, oldest)
	}
}

// store keeps a bitmap that has landed, and drops the ones drawn least
// recently while the cache is over its budget. The caller holds the lock.
func (t *thumbCache) store(url string, entry *thumb) {
	if old, ok := t.bitmaps[url]; ok {
		t.forgetLocked(url, old)
	}
	entry.url = url
	entry.element = t.recent.PushFront(entry)
	t.bitmaps[url] = entry
	t.touch(entry)
	t.held += entry.bytes
	base := sizeless(url)
	t.sizes[base] = append(t.sizes[base], url)
	for t.held > thumbBudget && len(t.bitmaps) > 1 {
		t.evictLeastRecent(url)
	}
}

// evictLeastRecent drops the bitmap that went longest without being drawn,
// other than keep, which has only just landed.
func (t *thumbCache) evictLeastRecent(keep string) {
	for element := t.recent.Back(); element != nil; element = element.Prev() {
		entry := element.Value.(*thumb)
		if entry.url != keep {
			t.forgetLocked(entry.url, entry)
			return
		}
	}
}

// forgetLocked removes a bitmap from every index. The caller holds the lock.
func (t *thumbCache) forgetLocked(url string, entry *thumb) {
	t.held -= entry.bytes
	delete(t.bitmaps, url)
	if entry.element != nil {
		t.recent.Remove(entry.element)
		entry.element = nil
	}
	base := sizeless(url)
	kept := t.sizes[base][:0]
	for _, other := range t.sizes[base] {
		if other != url {
			kept = append(kept, other)
		}
	}
	if len(kept) == 0 {
		delete(t.sizes, base)
	} else {
		t.sizes[base] = kept
	}
}

var (
	thumbnailSizePattern  = regexp.MustCompile(`=w\d+-h\d+`)
	thumbnailScalePattern = regexp.MustCompile(`=s\d+`)
)

// thumbnailURL asks Google's image host for a picture size instead of the
// one the response offered, which is often far larger than the element
// showing it.
func thumbnailURL(url string, size int) string {
	if thumbnailSizePattern.MatchString(url) {
		return thumbnailSizePattern.ReplaceAllString(url, "=w"+strconv.Itoa(size)+"-h"+strconv.Itoa(size))
	}
	if thumbnailScalePattern.MatchString(url) {
		return thumbnailScalePattern.ReplaceAllString(url, "=s"+strconv.Itoa(size))
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		return url
	}
	variant, ext, ok := youtubeThumbnailVariant(parsed)
	if !ok {
		return url
	}
	wanted := "hqdefault"
	if size <= 128 {
		wanted = "default"
	} else if size <= 320 {
		wanted = "mqdefault"
	}
	if youtubeVariantRank(variant) > youtubeVariantRank(wanted) {
		parsed.Path = path.Join(path.Dir(parsed.Path), wanted+ext)
		return parsed.String()
	}
	return url
}

// youtubeThumbnailVariant finds a standard YouTube thumbnail filename.
func youtubeThumbnailVariant(parsed *neturl.URL) (variant, ext string, ok bool) {
	host := parsed.Hostname()
	if host != "ytimg.com" && !strings.HasSuffix(host, ".ytimg.com") {
		return "", "", false
	}
	ext = path.Ext(parsed.Path)
	if ext != ".jpg" && ext != ".webp" {
		return "", "", false
	}
	variant = strings.TrimSuffix(path.Base(parsed.Path), ext)
	return variant, ext, youtubeVariantRank(variant) >= 0
}

// youtubeVariantRank orders the standard thumbnail sizes, from smallest to
// largest, so requests only downgrade a source that is larger than needed.
func youtubeVariantRank(variant string) int {
	switch variant {
	case "default":
		return 0
	case "mqdefault":
		return 1
	case "hqdefault":
		return 2
	case "sddefault":
		return 3
	case "hq720":
		return 4
	case "maxresdefault":
		return 5
	default:
		return -1
	}
}

// sizeless is url without the size Google's image host was asked for.
func sizeless(url string) string {
	if thumbnailSizePattern.MatchString(url) {
		return thumbnailSizePattern.ReplaceAllString(url, "=")
	}
	if thumbnailScalePattern.MatchString(url) {
		return thumbnailScalePattern.ReplaceAllString(url, "=")
	}
	parsed, err := neturl.Parse(url)
	if err != nil {
		return url
	}
	if _, ext, ok := youtubeThumbnailVariant(parsed); ok {
		parsed.Path = path.Join(path.Dir(parsed.Path), "thumbnail"+ext)
		return parsed.String()
	}
	return url
}

// colour returns the colour that stands out in the artwork at url, asked for
// at size pixels across, once the artwork has landed. The colour is taken
// from the picture the first time something asks, so covers that never need
// it never pay for it.
func (t *thumbCache) colour(url string, size int) (ui.Color, bool) {
	if url == "" {
		return ui.Color{}, false
	}
	t.mu.Lock()
	sized := t.keyLocked(url, size).sized
	entry, ok := t.bitmaps[sized]
	if !ok {
		t.mu.Unlock()
		return ui.Color{}, false
	}
	if entry.colourReady {
		colour, hasColour := entry.colour, entry.hasColour
		t.mu.Unlock()
		return colour, hasColour
	}
	data, ok := t.source(sized)
	t.mu.Unlock()
	if !ok {
		return ui.Color{}, false
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ui.Color{}, false
	}
	colour, hasColour := dominantColour(img)
	t.mu.Lock()
	if current, ok := t.bitmaps[sized]; ok && current == entry {
		current.colour, current.hasColour, current.colourReady = colour, hasColour, true
	}
	t.mu.Unlock()
	return colour, hasColour
}

// dominantColour finds the colour of a picture that a theme should grow from:
// the most vivid hue among the pixels that are neither near black nor near
// white, weighted by how much of the picture it fills and how saturated it is.
func dominantColour(img image.Image) (ui.Color, bool) {
	const bins, grid = 24, 28
	var weight [bins]float64
	var sumR, sumG, sumB [bins]float64
	b := img.Bounds()
	for gy := 0; gy < grid; gy++ {
		for gx := 0; gx < grid; gx++ {
			x := b.Min.X + (gx*2+1)*b.Dx()/(grid*2)
			y := b.Min.Y + (gy*2+1)*b.Dy()/(grid*2)
			r, g, bl, _ := img.At(x, y).RGBA()
			colour := ui.RGB(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
			hue, chroma := m3.HueOf(colour)
			light := float64(colour.R)*0.2126 + float64(colour.G)*0.7152 + float64(colour.B)*0.0722
			if chroma < 0.04 || light < 28 || light > 238 {
				continue
			}
			bin := int(hue/360*bins) % bins
			w := chroma * chroma
			weight[bin] += w
			sumR[bin] += float64(colour.R) * w
			sumG[bin] += float64(colour.G) * w
			sumB[bin] += float64(colour.B) * w
		}
	}
	best := -1
	for i := range weight {
		// A hue spreads over its neighbours, so a smooth gradient wins over
		// one stray pixel.
		w := weight[i] + 0.5*(weight[(i+1)%bins]+weight[(i+bins-1)%bins])
		if w > 0 && (best < 0 || w > weight[best]+0.5*(weight[(best+1)%bins]+weight[(best+bins-1)%bins])) {
			best = i
		}
	}
	if best < 0 || weight[best] == 0 {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(sumR[best]/weight[best]), uint8(sumG[best]/weight[best]), uint8(sumB[best]/weight[best])), true
}
