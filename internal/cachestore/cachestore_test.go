package cachestore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "cache.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Put("k1", Entry{Name: "cachedContents/abc", Model: "gemini-2.5-flash", ExpiresAt: time.Now().Add(1 * time.Hour)})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := s2.Get("k1")
	if !ok {
		t.Fatal("entry not found after reload")
	}
	if e.Name != "cachedContents/abc" || e.Model != "gemini-2.5-flash" {
		t.Errorf("unexpected entry after reload: %+v", e)
	}
}

func TestExpiredPrunedOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Put("fresh", Entry{Name: "a", ExpiresAt: time.Now().Add(1 * time.Hour)})
	s.Put("stale", Entry{Name: "b", ExpiresAt: time.Now().Add(-1 * time.Hour)})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Get("fresh"); !ok {
		t.Error("fresh entry missing after reload")
	}
	if _, ok := s2.Get("stale"); ok {
		t.Error("stale entry should have been pruned on load")
	}
}

func TestExpiredPrunedOnGet(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Put("will-expire", Entry{Name: "x", ExpiresAt: time.Now().Add(10 * time.Millisecond)})
	time.Sleep(20 * time.Millisecond)
	if _, ok := s.Get("will-expire"); ok {
		t.Error("Get should reject expired entry")
	}
}

func TestMissingFileIsEmpty(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should not error, got: %v", err)
	}
	if _, ok := s.Get("anything"); ok {
		t.Error("new store should be empty")
	}
}

func TestCorruptedFileRecovers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.json")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if s == nil {
		t.Fatalf("Open must return a usable store even on corruption, got nil (err=%v)", err)
	}
	if err == nil {
		t.Error("Open should report the parse error alongside the empty store")
	}
	// Store should be empty and still usable for Put/Save.
	if _, ok := s.Get("anything"); ok {
		t.Error("recovered store should be empty")
	}
	s.Put("k", Entry{Name: "n", ExpiresAt: time.Now().Add(1 * time.Hour)})
	if err := s.Save(); err != nil {
		t.Fatalf("Save on recovered store failed: %v", err)
	}
}

func TestAtomicWrite(t *testing.T) {
	// Verify no temp files linger after successful Save.
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "cache.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.Put("k", Entry{Name: "n", ExpiresAt: time.Now().Add(1 * time.Hour)})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && e.Name() != "cache.json" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
