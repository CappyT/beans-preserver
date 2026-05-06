package cache

import (
	"encoding/json"
	"os"
	"time"

	"go.etcd.io/bbolt"
)

const (
	bucketStats   = "stats"
	bucketMeta    = "meta"
	keyStartedAt  = "started_at"
)

// StatEvent is what the runner emits after each tool call.
type StatEvent struct {
	Tool          string
	CacheHit      bool
	Failed        bool
	// ServerFetched is true when the tool retrieved its content itself
	// (e.g. local_fetch on a URL). Inline-content tools (filter, extract,
	// summarize, transform) receive content via call args — those calls don't
	// actually save Claude tokens because Claude paid for the content in the
	// outbound call. Tracking the distinction lets the stats tool surface
	// realised vs theoretical savings.
	ServerFetched bool
	WallMs        int64
	InputTokens   int
	OutputTokens  int
	RawEstimate   int
}

// ToolStats is the persisted aggregate per tool name.
type ToolStats struct {
	Name                     string    `json:"name"`
	Calls                    int64     `json:"calls"`
	CacheHits                int64     `json:"cache_hits"`
	Errors                   int64     `json:"errors"`
	TotalWallMs              int64     `json:"total_wall_ms"`
	TotalInputTokens         int64     `json:"total_input_tokens"`
	TotalOutputTokens        int64     `json:"total_output_tokens"`
	TotalRawEstFetched       int64     `json:"total_raw_est_fetched"`
	TotalOutputTokensFetched int64     `json:"total_output_tokens_fetched"`
	TotalRawEstInline        int64     `json:"total_raw_est_inline"`
	LastCallAt               time.Time `json:"last_call_at,omitempty"`
}

func (c *Cache) initStats() error {
	return c.db.Update(func(tx *bbolt.Tx) error {
		if _, err := tx.CreateBucketIfNotExists([]byte(bucketStats)); err != nil {
			return err
		}
		mb, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
		if err != nil {
			return err
		}
		if mb.Get([]byte(keyStartedAt)) == nil {
			ts, _ := time.Now().UTC().MarshalBinary()
			return mb.Put([]byte(keyStartedAt), ts)
		}
		return nil
	})
}

// RecordCall persists one StatEvent. Called once per tool invocation.
func (c *Cache) RecordCall(ev StatEvent) error {
	return c.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketStats))
		if b == nil {
			return nil
		}
		var s ToolStats
		if raw := b.Get([]byte(ev.Tool)); raw != nil {
			_ = json.Unmarshal(raw, &s)
		}
		s.Name = ev.Tool
		s.Calls++
		if ev.CacheHit {
			s.CacheHits++
		}
		if ev.Failed {
			s.Errors++
		}
		s.TotalWallMs += ev.WallMs
		s.TotalInputTokens += int64(ev.InputTokens)
		s.TotalOutputTokens += int64(ev.OutputTokens)
		if ev.ServerFetched {
			s.TotalRawEstFetched += int64(ev.RawEstimate)
			s.TotalOutputTokensFetched += int64(ev.OutputTokens)
		} else {
			s.TotalRawEstInline += int64(ev.RawEstimate)
		}
		s.LastCallAt = time.Now().UTC()
		data, err := json.Marshal(&s)
		if err != nil {
			return err
		}
		return b.Put([]byte(ev.Tool), data)
	})
}

// AllStats returns a snapshot of every recorded tool's stats.
func (c *Cache) AllStats() (map[string]ToolStats, error) {
	out := map[string]ToolStats{}
	err := c.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketStats))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var s ToolStats
			if json.Unmarshal(v, &s) == nil {
				out[string(k)] = s
			}
			return nil
		})
	})
	return out, err
}

// StartedAt is the server-lifecycle anchor — first time the cache was opened
// without a meta record. Survives restarts; only ResetStats clears it.
func (c *Cache) StartedAt() (time.Time, error) {
	var ts time.Time
	err := c.db.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketMeta))
		if b == nil {
			return nil
		}
		raw := b.Get([]byte(keyStartedAt))
		if raw == nil {
			return nil
		}
		return ts.UnmarshalBinary(raw)
	})
	return ts, err
}

// ResetStats wipes counters and re-anchors StartedAt to now.
func (c *Cache) ResetStats() error {
	return c.db.Update(func(tx *bbolt.Tx) error {
		_ = tx.DeleteBucket([]byte(bucketStats))
		_ = tx.DeleteBucket([]byte(bucketMeta))
		if _, err := tx.CreateBucket([]byte(bucketStats)); err != nil {
			return err
		}
		mb, err := tx.CreateBucket([]byte(bucketMeta))
		if err != nil {
			return err
		}
		ts, _ := time.Now().UTC().MarshalBinary()
		return mb.Put([]byte(keyStartedAt), ts)
	})
}

// CacheDiagnostics returns count of data entries and the on-disk size of the
// underlying bbolt file. Errors are swallowed and reported as zeros.
func (c *Cache) CacheDiagnostics() (entries int, bytes int64) {
	_ = c.db.View(func(tx *bbolt.Tx) error {
		if b := tx.Bucket([]byte(bucketData)); b != nil {
			entries = b.Stats().KeyN
		}
		return nil
	})
	if info, err := os.Stat(c.db.Path()); err == nil {
		bytes = info.Size()
	}
	return entries, bytes
}
