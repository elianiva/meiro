package cookies

import (
	"context"
	"regexp"
	"strings"
)

type Store interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
}

func Header(pasted string) string {
	text := strings.TrimSpace(pasted)
	if at := cookieName.FindStringIndex(text); at != nil {
		text = text[at[1]:]
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "'\"\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

var cookieName = regexp.MustCompile(`(?i)cookie:`)
