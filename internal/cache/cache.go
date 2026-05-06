package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"go.etcd.io/bbolt"
)

const bucketData = "data"

type Cache struct {
	db         *bbolt.DB
	maxEntries int
}

type entry struct {
	Value    []byte    `json:"v"`
	Created  time.Time `json:"c"`
	Accessed time.Time `json:"a"`
	ExpireAt time.Time `json:"e,omitempty"`
}

func Open(path string, maxEntries int) (*Cache, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o644, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("open cache db: %w", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists([]byte(bucketData))
		return e
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	c := &Cache{db: db, maxEntries: maxEntries}
	if err := c.initStats(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return c, nil
}

func (c *Cache) Close() error { return c.db.Close() }

// Key builds a deterministic cache key from a tool name and any number of input parts.
// Each part is joined with a NUL separator, then SHA-256'd.
func Key(tool string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(tool))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) Get(key string) ([]byte, bool) {
	var (
		value []byte
		hit   bool
	)
	_ = c.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		raw := b.Get([]byte(key))
		if raw == nil {
			return nil
		}
		var e entry
		if err := json.Unmarshal(raw, &e); err != nil {
			return b.Delete([]byte(key))
		}
		if !e.ExpireAt.IsZero() && time.Now().After(e.ExpireAt) {
			return b.Delete([]byte(key))
		}
		e.Accessed = time.Now()
		if data, err := json.Marshal(e); err == nil {
			_ = b.Put([]byte(key), data)
		}
		value = e.Value
		hit = true
		return nil
	})
	return value, hit
}

func (c *Cache) Put(key string, value []byte, ttl time.Duration) error {
	return c.db.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket([]byte(bucketData))
		now := time.Now()
		e := entry{Value: value, Created: now, Accessed: now}
		if ttl > 0 {
			e.ExpireAt = now.Add(ttl)
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if err := b.Put([]byte(key), data); err != nil {
			return err
		}
		return c.evictIfNeeded(tx)
	})
}

// evictIfNeeded keeps the bucket below maxEntries by dropping the oldest-accessed
// entries first. We can't trust b.Stats().KeyN here — inside the same transaction
// it still reflects the committed state, so a fresh Put isn't counted. Iterate
// instead. Full scan is fine for the cap range we expect (≤ 10k).
func (c *Cache) evictIfNeeded(tx *bbolt.Tx) error {
	if c.maxEntries <= 0 {
		return nil
	}
	b := tx.Bucket([]byte(bucketData))
	type pair struct {
		key      []byte
		accessed time.Time
	}
	var all []pair
	_ = b.ForEach(func(k, v []byte) error {
		var e entry
		if json.Unmarshal(v, &e) == nil {
			kk := make([]byte, len(k))
			copy(kk, k)
			all = append(all, pair{kk, e.Accessed})
		}
		return nil
	})
	if len(all) <= c.maxEntries {
		return nil
	}
	sort.Slice(all, func(i, j int) bool { return all[i].accessed.Before(all[j].accessed) })
	for i := 0; i < len(all)-c.maxEntries; i++ {
		if err := b.Delete(all[i].key); err != nil {
			return err
		}
	}
	return nil
}
