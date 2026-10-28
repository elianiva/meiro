package cookies

import (
	"context"
	"regexp"
	"strings"
)

// Store keeps the sign-in between runs. Load fails with
// secretfile.ErrNotSaved when nothing is saved.
type Store interface {
	Load(ctx context.Context) (string, error)
	Save(ctx context.Context, cookie string) error
	Delete(ctx context.Context) error
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
