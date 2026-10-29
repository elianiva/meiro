package lastfm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func play(title string, started time.Time) Scrobble {
	return Scrobble{Track: Track{Artist: "A", Title: title, Album: "Al", Duration: 200 * time.Second}, Started: started, Chosen: title == "keep"}
}

func TestOutboxDropsPlaysLastFmWouldIgnore(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	o := &Outbox{}
	o.Add(play("old", now.Add(-14*24*time.Hour)))
	o.Add(play("new", now.Add(-time.Hour)))
	batch := o.Batch(now)
	if len(batch) != 1 || batch[0].Title != "new" || o.Len() != 1 {
		t.Errorf("batch = %+v, len %d", batch, o.Len())
	}
}

func TestOutboxBatchesAtLastFmsLimitAndCapsItself(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	o := &Outbox{}
	for i := range MaxPending + 5 {
		o.Add(play("t", now.Add(time.Duration(i-MaxPending-10)*time.Second)))
	}
	if o.Len() != MaxPending {
		t.Fatalf("Len = %d, want the cap %d", o.Len(), MaxPending)
	}
	batch := o.Batch(now)
	if len(batch) != MaxBatch {
		t.Fatalf("batch = %d, want %d", len(batch), MaxBatch)
	}
	if want := now.Add(time.Duration(5-MaxPending-10) * time.Second); !batch[0].Started.Equal(want) {
		t.Errorf("oldest kept = %v, want %v: the oldest plays go first", batch[0].Started, want)
	}
}

func TestOutboxToleratesBadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pending.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := OpenOutbox(path)
	if o.Len() != 0 {
		t.Error("an unreadable file is an empty outbox")
	}
	o.Add(play("x", time.Now()))
	o.Wait()
	if OpenOutbox(path).Len() != 1 {
		t.Error("the outbox did not replace the bad file")
	}
}
