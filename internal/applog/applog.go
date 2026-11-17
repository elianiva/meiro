package applog

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

const (
	FileName = "meiro.log"
	MaxSize  = 1 << 20
	Keep     = 3
)

type File struct {
	mu     sync.Mutex
	dir    string
	max    int64
	keep   int
	header func() string
	file   *os.File
	size   int64
}

func Open(dir string, header func() string) (*File, error) {
	return open(dir, MaxSize, Keep, header)
}

func open(dir string, maxSize int64, keep int, header func() string) (*File, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f := &File{dir: dir, max: maxSize, keep: keep, header: header}
	if err := f.start(false); err != nil {
		return nil, err
	}
	return f, nil
}

func (f *File) Dir() string { return f.dir }

func (f *File) Path() string { return filepath.Join(f.dir, FileName) }

func (f *File) start(roll bool) error {
	path := f.Path()
	if roll {
		f.shift(path)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	f.file, f.size = file, info.Size()
	if f.size == 0 && f.header != nil {
		n, _ := io.WriteString(file, Redact(f.header())+"\n")
		f.size += int64(n)
	}
	return nil
}

func (f *File) shift(path string) {
	_ = os.Remove(fmt.Sprintf("%s.%d", path, f.keep))
	for i := f.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", path, i), fmt.Sprintf("%s.%d", path, i+1))
	}
	if f.keep > 0 {
		_ = os.Rename(path, path+".1")
	} else {
		_ = os.Remove(path)
	}
}

func (f *File) Write(p []byte) (int, error) {
	line := redactBytes(p)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return len(p), nil
	}
	if f.size > 0 && f.size+int64(len(line)) > f.max {
		_ = f.file.Close()
		f.file = nil
		if err := f.start(true); err != nil {
			return len(p), nil
		}
	}
	n, _ := f.file.Write(line)
	f.size += int64(n)
	return len(p), nil
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func Install(f *File) {
	log.SetOutput(tee{f: f, other: log.Writer()})
}

type tee struct {
	f     *File
	other io.Writer
}

func (t tee) Write(p []byte) (int, error) {
	_, _ = t.f.Write(p)
	_, _ = t.other.Write(p)
	return len(p), nil
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b((?:set-)?cookie|authorization|proxy-authorization)(\s*[:=]\s*)[^\r\n]*`),
	regexp.MustCompile(`\b((?:__Secure-|__Host-)?(?:[1-3]P)?(?:SAPISID|APISID|SID|HSID|SSID|SIDCC|LOGIN_INFO|PSID|PSIDCC|PSIDTS|PAPISID))(=)[^;\s"']+`),
	regexp.MustCompile(`(?i)\b(SAPISIDHASH|[\w-]*(?:token|secret|password|passwd|api[_-]?key))(\s*[:=]\s*)[^\s,;"'&]+`),
}

const Redacted = "<redacted>"

func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}${2}"+Redacted)
	}
	return s
}

func redactBytes(p []byte) []byte { return []byte(Redact(string(p))) }
