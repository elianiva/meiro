// Package artwork downloads, decodes and keeps the pictures the app shows:
// covers, avatars and the artwork of the track playing, at the sizes they are
// drawn at, within a memory budget.
package artwork

import (
	"bytes"
	"container/list"
	"context"
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
const thumbBudget = 48 << 20

// thumbSourceBudget bounds compressed pictures kept for rebuilding evicted
// bitmaps without downloading them again.
const thumbSourceBudget = 8 << 20

// Store keeps downloaded pictures beyond the memory the cache may use, so an
// evicted bitmap is rebuilt from a file and not downloaded again. A
// diskcache.Cache is one.
type Store interface {
	Get(key string) ([]byte, bool)
	Has(key string) bool
	Put(key string, data []byte)
}

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

// Cache keeps decoded artwork and a smaller cache of compressed sources. It
// is safe for concurrent use: the view asks it for pictures on the main
// thread while its workers download them.
type Cache struct {
	client *http.Client
	notify func()

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
	colouring  map[string]bool
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
	sizes   map[string][]string
	colours map[string]thumbColour
	// disk is where pictures are kept on disk, or nil.
	disk Store
}

// UseStore keeps the pictures it downloads in store, and reads them from
// there before downloading.
func (t *Cache) UseStore(store Store) {
	t.mu.Lock()
	t.disk = store
	t.mu.Unlock()
}

// thumbRequest is the (URL, size, corner) triple a lookup asks about. corner
// is the rounding of the corners, in thousandths of the picture's side.
type thumbRequest struct {
	url    string
	size   int
	corner int
}

// thumbKeys is the derivation of a thumbRequest. sized names the bitmap in
// every index: the URL asked for at that size, with its corner. base groups
// every size of one picture drawn with that corner. fetch is the URL to
// download, which is also what the compressed source is kept under, so
// bitmaps of one picture with different corners share it.
type thumbKeys struct {
	sized  string
	base   string
	fetch  string
	size   int
	corner int
}

// thumbJob is a picture waiting for a worker. A source means its compressed
// bytes are already at hand, so no download is needed.
type thumbJob struct {
	tick   uint64
	keys   thumbKeys
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
	// url, base and element place the bitmap in the cache's indexes and in
	// thumbCache.recent.
	url     string
	base    string
	element *list.Element
	bytes   int
	used    uint64
}

// thumbColour is the colour taken from a picture. It is only taken when
// something asks for it, so covers never pay for a theme seed they will not
// use. ready separates "not taken yet" from "taken and found nothing".
type thumbColour struct {
	colour ui.Color
	has    bool
	ready  bool
}

// thumbColours bounds the remembered colours.
const thumbColours = 256

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

// New returns a cache that downloads with client and calls notify when a
// bitmap lands, so the window draws it.
func New(client *http.Client, notify func()) *Cache {
	t := &Cache{
		client:    client,
		notify:    notify,
		bitmaps:   make(map[string]*thumb),
		recent:    list.New(),
		sources:   make(map[string]*thumbSource),
		queue:     make(map[string]thumbJob),
		pending:   make(map[string]bool),
		colouring: make(map[string]bool),
		failed:    make(map[string]thumbFailure),
		keys:      make(map[thumbRequest]thumbKeys),
		sizes:     make(map[string][]string),
		colours:   make(map[string]thumbColour),
	}
	t.cond = sync.NewCond(&t.mu)
	for range thumbFetches {
		go t.worker()
	}
	return t
}

// Corner is the corner rounding to ask Bitmap for when the picture is drawn
// side points across with a corner of radius points.
func Corner(radius, side float32) float32 {
	if side <= 0 {
		return 0
	}
	return min(max(radius/side, 0), 0.5)
}

// Bitmap returns the artwork at url, asked for at size pixels across. The
// picture is cropped square, shrunk to size when it is larger, and has its
// corners rounded by corner, a fraction of its side up to 0.5 (see Corner),
// so that drawing it needs no clip. While it downloads or rebuilds from a
// compressed source, it returns another size of the same picture and corner
// when one is loaded, and nil otherwise.
func (t *Cache) Bitmap(url string, size int, corner float32) *ui.Bitmap {
	if url == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := t.keyLocked(url, size, corner)
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
	} else if source, ok := t.source(key.fetch); ok {
		t.pending[key.sized] = true
		t.enqueueLocked(key, source)
	} else if t.retryDue(key.sized) {
		t.pending[key.sized] = true
		t.enqueueLocked(key, nil)
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
func (t *Cache) keyLocked(url string, size int, corner float32) thumbKeys {
	permille := int(corner*1000 + 0.5)
	request := thumbRequest{url: url, size: size, corner: permille}
	if key, ok := t.keys[request]; ok {
		return key
	}
	fetch := thumbnailURL(url, size)
	suffix := "\x00" + strconv.Itoa(permille)
	key := thumbKeys{sized: fetch + suffix, base: sizeless(fetch) + suffix, fetch: fetch, size: size, corner: permille}
	if len(t.keys) >= thumbKeyMemo {
		t.keys = make(map[thumbRequest]thumbKeys)
	}
	t.keys[request] = key
	return key
}

// retryDue reports whether a missing picture may be asked for again. A
// permanent failure never may; a transient one waits out its backoff. The
// caller holds the lock.
func (t *Cache) retryDue(url string) bool {
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
func (t *Cache) touch(entry *thumb) *ui.Bitmap {
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
func (t *Cache) source(url string) ([]byte, bool) {
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
func (t *Cache) worker() {
	for {
		t.mu.Lock()
		for len(t.queue) == 0 {
			t.cond.Wait()
		}
		job := t.popLocked()
		store := t.disk
		t.mu.Unlock()
		if job.source != nil {
			t.decode(job.keys, job.source, false)
			continue
		}
		if store != nil {
			if data, ok := store.Get(job.keys.fetch); ok {
				t.decode(job.keys, data, true)
				continue
			}
		}
		data, err := t.download(job.keys.fetch)
		if err != nil {
			t.failedFetch(job.keys.sized, err)
			continue
		}
		if store != nil {
			store.Put(job.keys.fetch, data)
		}
		t.decode(job.keys, data, true)
	}
}

// enqueueLocked adds a picture to the work queue, dropping the least recently
// wanted wait when the queue is full. The caller holds the lock.
func (t *Cache) enqueueLocked(keys thumbKeys, source []byte) {
	if _, queued := t.queue[keys.sized]; !queued && len(t.queue) >= thumbQueueLimit {
		t.dropStaleLocked()
	}
	t.seq++
	t.queue[keys.sized] = thumbJob{tick: t.seq, keys: keys, source: source}
	t.pending[keys.sized] = true
	t.cond.Broadcast()
}

// popLocked takes the most recently wanted job, so visible pictures are not
// starved by stale waits queued behind them. The caller holds the lock.
func (t *Cache) popLocked() thumbJob {
	url, found := "", false
	for key, job := range t.queue {
		if !found || job.tick > t.queue[url].tick {
			url, found = key, true
		}
	}
	job := t.queue[url]
	delete(t.queue, url)
	return job
}

// dropStaleLocked forgets the least recently wanted wait and lets a later
// frame ask for it again only if it is still wanted. The caller holds the
// lock.
func (t *Cache) dropStaleLocked() {
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
func (t *Cache) download(url string) ([]byte, error) {
	// Each download is bounded by the client's timeout; nothing cancels one
	// sooner, since a picture that lands late is still kept.
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
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
	// The buffer grew in doublings and the kept copy would carry its spare
	// room, for as long as the source stays cached.
	return bytes.Clone(data), nil
}

// decode creates a bitmap, retaining downloaded source bytes. The colour is
// taken later, when something asks for it. Package ui registers the JPEG, PNG,
// GIF, WebP and BMP decoders with image.Decode, and this package imports ui,
// so the artwork formats YouTube serves all decode here.
func (t *Cache) decode(keys thumbKeys, data []byte, keepSource bool) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.failedFetch(keys.sized, fmt.Errorf("%w: %w", errDecode, err))
		return
	}
	// The pixels are drawn as they are: the picture is already the size and
	// shape it is shown at, which the bitmap takes without copying.
	bitmap := ui.NewBitmap(fit(img, keys.size, float64(keys.corner)/1000))
	url := keys.sized
	t.mu.Lock()
	delete(t.pending, url)
	delete(t.failed, url)
	if keepSource {
		t.storeSource(keys.fetch, data)
	}
	t.store(url, &thumb{bitmap: bitmap, base: keys.base, bytes: thumbBytes(bitmap)})
	t.mu.Unlock()
	t.notify()
}

// failedFetch notes a failed download or decode and schedules a redraw for
// the next attempt. A permanent failure is never retried, and a transient one
// waits out an ever longer backoff.
func (t *Cache) failedFetch(url string, err error) {
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
func (t *Cache) pruneFailedLocked() {
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
func (t *Cache) storeSource(url string, data []byte) {
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
func (t *Cache) store(url string, entry *thumb) {
	if old, ok := t.bitmaps[url]; ok {
		t.forgetLocked(url, old)
	}
	entry.url = url
	entry.element = t.recent.PushFront(entry)
	t.bitmaps[url] = entry
	t.touch(entry)
	t.held += entry.bytes
	t.sizes[entry.base] = append(t.sizes[entry.base], url)
	for t.held > thumbBudget && len(t.bitmaps) > 1 {
		t.evictLeastRecent(url)
	}
}

// evictLeastRecent drops the bitmap that went longest without being drawn,
// other than keep, which has only just landed.
func (t *Cache) evictLeastRecent(keep string) {
	for element := t.recent.Back(); element != nil; element = element.Prev() {
		if entry, ok := element.Value.(*thumb); ok && entry.url != keep {
			t.forgetLocked(entry.url, entry)
			return
		}
	}
}

// forgetLocked removes a bitmap from every index. The caller holds the lock.
func (t *Cache) forgetLocked(url string, entry *thumb) {
	t.held -= entry.bytes
	delete(t.bitmaps, url)
	if entry.element != nil {
		t.recent.Remove(entry.element)
		entry.element = nil
	}
	kept := t.sizes[entry.base][:0]
	for _, other := range t.sizes[entry.base] {
		if other != url {
			kept = append(kept, other)
		}
	}
	if len(kept) == 0 {
		delete(t.sizes, entry.base)
	} else {
		t.sizes[entry.base] = kept
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

// Colour returns the colour that stands out in the artwork at url, asked for
// at size pixels across, once the artwork has landed. The colour is taken
// from the picture the first time something asks, so covers that never need
// it never pay for it. It returns ready false while the artwork or its colour
// is still on the way, and the cache wakes the view when it lands.
func (t *Cache) Colour(url string, size int) (colour ui.Color, has, ready bool) {
	if url == "" {
		return ui.Color{}, false, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	fetch := t.keyLocked(url, size, 0).fetch
	if taken, ok := t.colours[fetch]; ok && taken.ready {
		return taken.colour, taken.has, true
	}
	data, ok := t.source(fetch)
	if !ok && (t.disk == nil || !t.disk.Has(fetch)) {
		return ui.Color{}, false, false
	}
	if !t.colouring[fetch] {
		// Decoding and quantizing is too much for a frame: do it on a
		// worker and wake the view when it is done.
		t.colouring[fetch] = true
		go t.takeColour(fetch, data, t.disk)
	}
	return ui.Color{}, false, false
}

// takeColour finds the colour of a picture off the main thread and files it.
func (t *Cache) takeColour(fetch string, data []byte, store Store) {
	var colour ui.Color
	var has bool
	if data == nil && store != nil {
		data, _ = store.Get(fetch)
	}
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		colour, has = dominantColour(img)
	}
	t.mu.Lock()
	delete(t.colouring, fetch)
	if len(t.colours) >= thumbColours {
		t.colours = make(map[string]thumbColour)
	}
	t.colours[fetch] = thumbColour{colour: colour, has: has, ready: true}
	t.mu.Unlock()
	t.notify()
}

// dominantColour finds the colour of a picture that a theme should grow from:
// the most vivid hue among the pixels that are neither near black nor near
// white, weighted by how much of the picture it fills and how saturated it is.
func dominantColour(img image.Image) (ui.Color, bool) {
	const bins, grid = 24, 28
	var weight [bins]float64
	var sumR, sumG, sumB [bins]float64
	b := img.Bounds()
	for gy := range grid {
		for gx := range grid {
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

// Stats is how much the cache holds, for measuring that it stays bounded.
type Stats struct {
	// Bitmaps and Bytes are the decoded pictures kept, and their pixels.
	Bitmaps, Bytes int
	// Sources and SourceBytes are the compressed pictures kept for rebuilding
	// evicted bitmaps.
	Sources, SourceBytes int
	// Queued, Failed, Keys and Colours are the sizes of the other indexes.
	Queued, Failed, Keys, Colours int
}

// Stats reports what the cache holds now.
func (t *Cache) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{
		Bitmaps: len(t.bitmaps), Bytes: t.held,
		Sources: len(t.sources), SourceBytes: t.sourceHeld,
		Queued: len(t.queue), Failed: len(t.failed), Keys: len(t.keys), Colours: len(t.colours),
	}
}
