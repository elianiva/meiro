package cookies

import (
	"context"
	"errors"
	"os"
	"regexp"
	"strings"

	"github.com/elianiva/meiro/internal/fsutil"
)

// ErrNotSaved reports that no cookie is saved.
var ErrNotSaved = errors.New("not signed in")

// Store keeps the sign-in between runs.
type Store interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
}

// FileStore keeps the cookie in a file only its user can read. Creating one
// does no work, so it is cheap on the main thread.
type FileStore struct {
	Path string
}

func (s *FileStore) Load(context.Context) (string, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotSaved
	}
	if err != nil {
		return "", err
	}
	cookie := strings.TrimSpace(string(data))
	if cookie == "" {
		return "", ErrNotSaved
	}
	return cookie, nil
}

func (s *FileStore) Save(_ context.Context, cookie string) error {
	// The cookie is a credential: keep it readable by this user only.
	return fsutil.WriteFileAtomic(s.Path, []byte(cookie), 0o600)
}

func (s *FileStore) Delete(context.Context) error {
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Header picks the cookie out of what the user pasted: the value of the
// Cookie header alone, the header with its name, or a whole line copied as a
// cURL command.
func Header(pasted string) string {
	text := strings.TrimSpace(pasted)
	// The name is matched on the text itself: lowercasing a copy can change
	// its length, and the offsets would no longer fit.
	if at := cookieName.FindStringIndex(text); at != nil {
		text = text[at[1]:]
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "'\"\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// cookieName is where the value of a Cookie header starts, whether the text
// is the header, or a cURL command with it as an argument.
var cookieName = regexp.MustCompile(`(?i)cookie:`)
