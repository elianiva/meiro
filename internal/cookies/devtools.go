package cookies

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// devtools speaks the Chrome DevTools Protocol over one WebSocket, one
// request at a time, which is all reading cookies and closing the browser
// need.
type devtools struct {
	ws   *wsConn
	next int
}

type devtoolsCookie struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	Domain  string  `json:"domain"`
	Expires float64 `json:"expires"`
}

// call sends a command and waits for its answer, skipping events.
func (d *devtools) call(ctx context.Context, method string, result any) error {
	defer d.ws.limit(ctx)()
	d.next++
	id := d.next
	request, _ := json.Marshal(map[string]any{"id": id, "method": method})
	if err := d.ws.writeText(request); err != nil {
		return err
	}
	for {
		raw, err := d.ws.readMessage()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		var reply struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &reply) != nil || reply.ID != id {
			continue
		}
		if reply.Error != nil {
			return fmt.Errorf("browser: %s: %s", method, reply.Error.Message)
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(reply.Result, result)
	}
}

// cookies reads every cookie the browser holds.
func (d *devtools) cookies(ctx context.Context) ([]devtoolsCookie, error) {
	var out struct {
		Cookies []devtoolsCookie `json:"cookies"`
	}
	if err := d.call(ctx, "Storage.getCookies", &out); err != nil {
		return nil, err
	}
	return out.Cookies, nil
}

// closeBrowser asks the browser to quit. It may drop the connection before
// it answers, and the caller stops the process either way.
func (d *devtools) closeBrowser(ctx context.Context) {
	_ = d.call(ctx, "Browser.close", nil)
}

// youtubeHeader builds the Cookie header a request to music.youtube.com
// carries from the cookies of a browser: those of youtube.com and its
// subdomains that have not expired. A later cookie of the same name wins.
func youtubeHeader(all []devtoolsCookie, now time.Time) string {
	var pairs []string
	seen := map[string]int{}
	for _, c := range all {
		domain := strings.TrimPrefix(c.Domain, ".")
		if domain != "youtube.com" && !strings.HasSuffix(domain, ".youtube.com") {
			continue
		}
		// Expires is -1 for a cookie of the session.
		if c.Expires > 0 && time.Unix(int64(c.Expires), 0).Before(now) {
			continue
		}
		pair := c.Name + "=" + c.Value
		if i, ok := seen[c.Name]; ok {
			pairs[i] = pair
			continue
		}
		seen[c.Name] = len(pairs)
		pairs = append(pairs, pair)
	}
	return strings.Join(pairs, "; ")
}
