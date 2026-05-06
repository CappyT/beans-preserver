package cache

import (
	"path/filepath"
	"testing"
	"time"
)

func openTmp(t *testing.T, max int) *Cache {
	t.Helper()
	c, err := Open(filepath.Join(t.TempDir(), "cache.db"), max)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestPutGet(t *testing.T) {
	c := openTmp(t, 100)
	if err := c.Put("k1", []byte("v1"), 0); err != nil {
		t.Fatal(err)
	}
	v, ok := c.Get("k1")
	if !ok || string(v) != "v1" {
		t.Errorf("got (%q,%v), want (v1,true)", string(v), ok)
	}
	if _, ok := c.Get("missing"); ok {
		t.Errorf("expected miss")
	}
}

func TestTTLExpiration(t *testing.T) {
	c := openTmp(t, 100)
	if err := c.Put("k", []byte("v"), 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("k"); !ok {
		t.Errorf("hit immediately, but missed")
	}
	time.Sleep(80 * time.Millisecond)
	if _, ok := c.Get("k"); ok {
		t.Errorf("expected expiration after TTL")
	}
}

func TestLRUEviction(t *testing.T) {
	c := openTmp(t, 3)
	for _, k := range []string{"a", "b", "c"} {
		_ = c.Put(k, []byte(k), 0)
	}
	// Touch "a" so it becomes most-recently-used.
	if _, ok := c.Get("a"); !ok {
		t.Fatalf("a should be present")
	}
	// Putting "d" should evict "b" (oldest accessed) — not "a".
	_ = c.Put("d", []byte("d"), 0)
	if _, ok := c.Get("a"); !ok {
		t.Errorf("a was wrongly evicted")
	}
	if _, ok := c.Get("b"); ok {
		t.Errorf("b should have been evicted")
	}
}

func TestKeyDeterministic(t *testing.T) {
	a := Key("filter", "model-x", "criterion", "content")
	b := Key("filter", "model-x", "criterion", "content")
	if a != b {
		t.Errorf("Key not deterministic: %s vs %s", a, b)
	}
	c := Key("filter", "model-y", "criterion", "content")
	if a == c {
		t.Errorf("Key collision across different models")
	}
}
