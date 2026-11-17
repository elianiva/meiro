package artwork

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
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
	xdraw "golang.org/x/image/draw"

	"github.com/elianiva/meiro/m3"
)

const thumbBudget = 24 << 20

const thumbSourceBudget = 4 << 20

type Store interface {
	Get(key string) ([]byte, bool)
	Has(key string) bool
	Put(key string, data []byte)
}

const thumbRetry = 10 * time.Second

const thumbRedrawSlack = time.Second

const thumbDownloadBufferGuess = 512 << 10

const thumbRetryMax = 5 * time.Minute

const thumbFailures = 256

const thumbQueueLimit = 128

const thumbKeyMemo = 4096

type Cache struct {
	client *http.Client
	notify func()

	mu         sync.Mutex
	cond       *sync.Cond
	closed     bool
	queue      map[string]thumbJob
	bitmaps    map[string]*thumb
	recent     *list.List
	sources    map[string]*thumbSource
	held       int
	sourceHeld int
	tick       uint64
	seq        uint64
	pending    map[string]bool
	colouring  map[string]bool
	keys       map[thumbRequest]thumbKeys
	failed     map[string]thumbFailure
	sizes      map[string][]string
	colours    map[string]thumbColour
	disk       Store
}

func (t *Cache) UseStore(store Store) {
	t.mu.Lock()
	t.disk = store
	t.mu.Unlock()
}

type thumbRequest struct {
	url  string
	size int
}

type thumbKeys struct {
	sized string
	base  string
	fetch string
	size  int
}

type thumbJob struct {
	tick   uint64
	keys   thumbKeys
	source []byte
}

type thumbFailure struct {
	at        time.Time
	attempts  int
	permanent bool
}

func (f thumbFailure) wait() time.Duration {
	wait := thumbRetry
	for i := 1; i < f.attempts && wait < thumbRetryMax; i++ {
		wait *= 2
	}
	return min(wait, thumbRetryMax)
}

var (
	errTooLarge = errors.New("picture is larger than the limit")
	errDecode   = errors.New("picture could not be decoded")
)

type statusError struct{ code int }

func (e statusError) Error() string { return "HTTP " + strconv.Itoa(e.code) }

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

type thumb struct {
	bitmap  *ui.Bitmap
	url     string
	base    string
	element *list.Element
	bytes   int
	used    uint64
}

type thumbColour struct {
	colour ui.Color
	has    bool
	ready  bool
}

const thumbColours = 256

type thumbSource struct {
	data []byte
	used uint64
}

func thumbBytes(b *ui.Bitmap) int {
	w, h := b.Size()
	const bytesPerPixel = 4
	const withMipmaps = 4. / 3
	return int(float64(w*h*bytesPerPixel) * withMipmaps)
}

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

func (t *Cache) Close() {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	t.cond.Broadcast()
}

func (t *Cache) Bitmap(url string, size int) *ui.Bitmap {
	if url == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	key := t.keyLocked(url, size)
	if entry, ok := t.bitmaps[key.sized]; ok {
		return t.touch(entry)
	}
	if t.pending[key.sized] {
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

func (t *Cache) keyLocked(url string, size int) thumbKeys {
	request := thumbRequest{url: url, size: size}
	if key, ok := t.keys[request]; ok {
		return key
	}
	fetch := thumbnailURL(url, size)
	key := thumbKeys{sized: fetch, base: sizeless(fetch), fetch: fetch, size: size}
	if len(t.keys) >= thumbKeyMemo {
		t.keys = make(map[thumbRequest]thumbKeys)
	}
	t.keys[request] = key
	return key
}

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

func (t *Cache) touch(entry *thumb) *ui.Bitmap {
	t.tick++
	entry.used = t.tick
	if entry.element != nil {
		t.recent.MoveToFront(entry.element)
	}
	return entry.bitmap
}

func (t *Cache) source(url string) ([]byte, bool) {
	entry, ok := t.sources[url]
	if !ok {
		return nil, false
	}
	t.tick++
	entry.used = t.tick
	return entry.data, true
}

const thumbFetches = 6

const thumbLimit = 8 << 20

func (t *Cache) worker() {
	for {
		t.mu.Lock()
		for len(t.queue) == 0 && !t.closed {
			t.cond.Wait()
		}
		if t.closed {
			t.mu.Unlock()
			return
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
		if t.decode(job.keys, data, true) && store != nil {
			store.Put(job.keys.fetch, data)
		}
	}
}

func (t *Cache) enqueueLocked(keys thumbKeys, source []byte) {
	if _, queued := t.queue[keys.sized]; !queued && len(t.queue) >= thumbQueueLimit {
		t.dropStaleLocked()
	}
	t.seq++
	t.queue[keys.sized] = thumbJob{tick: t.seq, keys: keys, source: source}
	t.pending[keys.sized] = true
	t.cond.Broadcast()
}

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

func (t *Cache) download(url string) ([]byte, error) {
	return t.downloadContext(context.Background(), url)
}

func (t *Cache) downloadContext(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	size := thumbDownloadBufferGuess
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
	withoutSpareCapacity := bytes.Clone(data)
	return withoutSpareCapacity, nil
}

func (t *Cache) NotificationIcon(ctx context.Context, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New("track has no artwork URL")
	}
	data, err := t.downloadContext(ctx, thumbnailURL(url, notificationIconSize))
	if err != nil {
		return nil, err
	}
	return notificationIconPNG(data)
}

const notificationIconSize = 96

func notificationIconPNG(data []byte) ([]byte, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("read notification artwork dimensions: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > 4096 || config.Height > 4096 {
		return nil, fmt.Errorf("notification artwork dimensions %dx%d are invalid", config.Width, config.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode notification artwork: %w", err)
	}
	cover := fit(img, notificationIconSize)
	icon := image.NewRGBA(image.Rect(0, 0, notificationIconSize, notificationIconSize))
	draw.Draw(icon, icon.Bounds(), image.NewUniform(color.RGBA{R: 32, G: 33, B: 36, A: 255}), image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(icon, icon.Bounds(), cover, cover.Bounds(), xdraw.Over, nil)

	var encoded bytes.Buffer
	if err := png.Encode(&encoded, icon); err != nil {
		return nil, fmt.Errorf("encode notification artwork: %w", err)
	}
	return encoded.Bytes(), nil
}

func (t *Cache) decode(keys thumbKeys, data []byte, keepSource bool) bool {
	bitmap, err := decodeBitmap(data, keys.size)
	if err != nil {
		t.failedFetch(keys.sized, fmt.Errorf("%w: %w", errDecode, err))
		return false
	}
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
	return true
}

func decodeBitmap(data []byte, side int) (*ui.Bitmap, error) {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width == config.Height && config.Width <= side {
		bitmap, err := ui.DecodeBitmap(data)
		if err != nil {
			return nil, err
		}
		bitmap.ReleasePixels()
		return bitmap, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return ui.NewBitmap(fit(img, side)), nil
}

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
	time.AfterFunc(wait+thumbRedrawSlack, t.notify)
}

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

func (t *Cache) evictLeastRecent(keep string) {
	for element := t.recent.Back(); element != nil; element = element.Prev() {
		if entry, ok := element.Value.(*thumb); ok && entry.url != keep {
			t.forgetLocked(entry.url, entry)
			return
		}
	}
}

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

func (t *Cache) Colour(url string) (colour ui.Color, has, ready bool) {
	if url == "" {
		return ui.Color{}, false, true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	base := sizeless(url)
	if taken, ok := t.colours[base]; ok && taken.ready {
		return taken.colour, taken.has, true
	}
	fetch, ok := t.colourSourceLocked(base)
	if !ok {
		return ui.Color{}, false, false
	}
	if !t.colouring[base] {
		t.colouring[base] = true
		data, _ := t.source(fetch)
		go t.takeColour(base, fetch, data, t.disk)
	}
	return ui.Color{}, false, false
}

func (t *Cache) colourSourceLocked(base string) (string, bool) {
	for _, fetch := range t.sizes[base] {
		if _, ok := t.sources[fetch]; ok {
			return fetch, true
		}
	}
	if t.disk != nil {
		for _, fetch := range t.sizes[base] {
			if t.disk.Has(fetch) {
				return fetch, true
			}
		}
	}
	return "", false
}

func (t *Cache) takeColour(base, fetch string, data []byte, store Store) {
	var colour ui.Color
	var has bool
	if data == nil && store != nil {
		data, _ = store.Get(fetch)
	}
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		colour, has = dominantColour(img)
	}
	t.mu.Lock()
	delete(t.colouring, base)
	if len(t.colours) >= thumbColours {
		t.colours = make(map[string]thumbColour)
	}
	t.colours[base] = thumbColour{colour: colour, has: has, ready: true}
	t.mu.Unlock()
	t.notify()
}

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
		smoothed := func(bin int) float64 {
			return weight[bin] + 0.5*(weight[(bin+1)%bins]+weight[(bin+bins-1)%bins])
		}
		if w := smoothed(i); w > 0 && (best < 0 || w > smoothed(best)) {
			best = i
		}
	}
	if best < 0 || weight[best] == 0 {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(sumR[best]/weight[best]), uint8(sumG[best]/weight[best]), uint8(sumB[best]/weight[best])), true
}

type Stats struct {
	Bitmaps, Bytes                int
	Sources, SourceBytes          int
	Queued, Failed, Keys, Colours int
}

func (t *Cache) Purge() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.bitmaps = make(map[string]*thumb)
	t.recent.Init()
	t.held = 0
	t.sources = make(map[string]*thumbSource)
	t.sourceHeld = 0
	t.sizes = make(map[string][]string)
	t.keys = make(map[thumbRequest]thumbKeys)
}

func (t *Cache) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{
		Bitmaps: len(t.bitmaps), Bytes: t.held,
		Sources: len(t.sources), SourceBytes: t.sourceHeld,
		Queued: len(t.queue), Failed: len(t.failed), Keys: len(t.keys), Colours: len(t.colours),
	}
}
