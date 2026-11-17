package diskcache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
)

const extension = ".bin"

type entry struct {
	size int64
	used time.Time
}

type Cache struct {
	dir   string
	limit int64

	mu    sync.Mutex
	files map[string]entry
	size  int64
}

func Open(dir string, limit int64) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	c := &Cache{dir: dir, limit: limit, files: make(map[string]entry)}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), extension) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		c.files[e.Name()] = entry{size: info.Size(), used: info.ModTime()}
		c.size += info.Size()
	}
	c.mu.Lock()
	c.trimLocked("")
	c.mu.Unlock()
	return c, nil
}

func name(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16]) + extension
}

func (c *Cache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.files[name(key)]
	return ok
}

func (c *Cache) Get(key string) ([]byte, bool) {
	file := name(key)
	c.mu.Lock()
	e, ok := c.files[file]
	if ok {
		e.used = time.Now()
		c.files[file] = e
	}
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(c.dir, file))
	if err != nil {
		c.mu.Lock()
		c.forgetLocked(file)
		c.mu.Unlock()
		return nil, false
	}
	_ = os.Chtimes(filepath.Join(c.dir, file), e.used, e.used)
	return data, true
}

func (c *Cache) Put(key string, data []byte) {
	if int64(len(data)) > c.limit {
		return
	}
	file := name(key)
	if err := fsutil.WriteFileAtomic(filepath.Join(c.dir, file), data, 0o600); err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.forgetLocked(file)
	c.files[file] = entry{size: int64(len(data)), used: time.Now()}
	c.size += int64(len(data))
	c.trimLocked(file)
}

func (c *Cache) forgetLocked(file string) {
	if old, ok := c.files[file]; ok {
		c.size -= old.size
		delete(c.files, file)
	}
}

func (c *Cache) trimLocked(keep string) {
	if c.size <= c.limit {
		return
	}
	headroom := c.limit / 10
	target := c.limit - headroom
	names := make([]string, 0, len(c.files))
	for file := range c.files {
		if file != keep {
			names = append(names, file)
		}
	}
	sort.Slice(names, func(i, j int) bool { return c.files[names[i]].used.Before(c.files[names[j]].used) })
	for _, file := range names {
		if c.size <= target {
			return
		}
		_ = os.Remove(filepath.Join(c.dir, file))
		c.forgetLocked(file)
	}
}
