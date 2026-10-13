// Package applog keeps the app's log in a file: a size-capped file in the
// logs folder that rolls over to a few older ones, with a header of the
// versions in use at the top of each, and with the secrets a log line could
// carry replaced before they are written.
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
	// FileName is the log being written. The older ones are FileName.1, the
	// newest, up to FileName.N for the oldest kept.
	FileName = "meiro.log"
	// MaxSize is how large a log file grows before the next one starts.
	MaxSize = 1 << 20
	// Keep is how many older files are kept next to the current one.
	Keep = 3
)

// File is a log file that rolls over when it reaches its size cap. It is an
// io.Writer that is safe for concurrent use; every line it is given has its
// secrets redacted first.
type File struct {
	mu     sync.Mutex
	dir    string
	max    int64
	keep   int
	header func() string
	file   *os.File
	size   int64
}

// Open opens dir/meiro.log for appending, creating dir for the user alone.
// header is called whenever a file is started, new or after a rollover, and
// what it returns is written first. A file left over from the last run is
// continued, and gets no second header.
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

// Dir is the folder the logs are in.
func (f *File) Dir() string { return f.dir }

// Path is the file being written.
func (f *File) Path() string { return filepath.Join(f.dir, FileName) }

// start opens the current file. With roll set, the files are shifted one
// place older first.
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

// shift renames meiro.log to meiro.log.1, meiro.log.1 to meiro.log.2, and so
// on, and drops the one that falls off the end.
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

// Write redacts p and appends it, starting the next file first when it would
// not fit. It never fails: a log that cannot be written must not take the
// app down with it, and the caller has no better place to report that.
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

// Close closes the file. Writes after it are dropped.
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

// Install sends the standard logger, and so slog's default handler, which
// writes through it, to f as well as to the writer they used until now.
func Install(f *File) {
	log.SetOutput(io.MultiWriter(log.Writer(), f))
}

// secretPatterns match what must never reach a log: cookie and authorization
// headers whole, the values of the session cookies YouTube uses, and
// key=value pairs that name a token or a password.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b((?:set-)?cookie|authorization|proxy-authorization)(\s*[:=]\s*)[^\r\n]*`),
	regexp.MustCompile(`\b((?:__Secure-|__Host-)?(?:[1-3]P)?(?:SAPISID|APISID|SID|HSID|SSID|SIDCC|LOGIN_INFO|PSID|PSIDCC|PSIDTS|PAPISID))(=)[^;\s"']+`),
	regexp.MustCompile(`(?i)\b(SAPISIDHASH|[\w-]*(?:token|secret|password|passwd|api[_-]?key))(\s*[:=]\s*)[^\s,;"'&]+`),
}

// Redacted is what stands in for a secret.
const Redacted = "<redacted>"

// Redact returns s with its secrets replaced.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "${1}${2}"+Redacted)
	}
	return s
}

func redactBytes(p []byte) []byte { return []byte(Redact(string(p))) }
