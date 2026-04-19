// Package cachestore provides a disk-backed store mapping content
// hashes to provider-side context-cache resource names (e.g. Gemini
// Context Caching). Entries carry an absolute expiry; expired entries
// are pruned on load.
//
// Concurrency: writes are atomic (temp-file + rename), so the file is
// never torn. However, the package does not coordinate concurrent
// read-modify-write cycles across processes: if two plancritic
// invocations race to add entries, the later Save overwrites the
// earlier one and that entry is lost from the local index. The
// provider-side cache resource still exists and will TTL out; the
// only cost is re-creating a cache on the next miss. This tradeoff
// avoids the cross-platform complexity of advisory file locking.
package cachestore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const storeVersion = 1

// Entry records a provider-side cache resource along with the model it
// was created for and its expiry time.
type Entry struct {
	Name      string    `json:"name"`
	Model     string    `json:"model"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Store is a JSON-file-backed map from content hash to Entry. All
// exposed methods are safe for concurrent use within a single process;
// cross-process coordination is out of scope (see package doc).
type Store struct {
	path    string
	mu      sync.Mutex
	entries map[string]Entry
}

type storeFile struct {
	Version int              `json:"version"`
	Entries map[string]Entry `json:"entries"`
}

// DefaultPath returns the standard on-disk location for the cache
// store, using os.UserCacheDir (which honors XDG_CACHE_HOME on Linux).
func DefaultPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("cachestore: user cache dir: %w", err)
	}
	return filepath.Join(dir, "plancritic", "gemini-cache.json"), nil
}

// Open loads the store at path. A missing file yields an empty store.
// A corrupted file (unparseable JSON) is treated as empty — the
// cache is a rebuildable index, and failing-hard here would block
// every future run until the user manually cleared it. The parse
// error is returned alongside the empty store so callers can log it
// via a separate channel (the store itself is still usable).
func Open(path string) (*Store, error) {
	s := &Store{path: path, entries: make(map[string]Entry)}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("cachestore: read %s: %w", path, err)
	}

	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return s, fmt.Errorf("cachestore: parse %s (starting fresh): %w", path, err)
	}

	now := time.Now()
	for k, e := range f.Entries {
		if e.ExpiresAt.After(now) {
			s.entries[k] = e
		}
	}
	return s, nil
}

// Get returns the entry for key. The second return is false if no live
// entry exists (absent or expired).
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return Entry{}, false
	}
	if !e.ExpiresAt.After(time.Now()) {
		delete(s.entries, key)
		return Entry{}, false
	}
	return e, true
}

// Put records an entry for key. Not persisted until Save is called.
func (s *Store) Put(key string, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = e
}

// Delete removes an entry by key.
func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// Save atomically writes the store to disk. Creates parent directories
// as needed. The write is performed via temp file + rename to avoid
// torn writes if a concurrent invocation reads mid-flight.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("cachestore: mkdir: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Entries: s.entries}, "", "  ")
	if err != nil {
		return fmt.Errorf("cachestore: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".cache-*.json")
	if err != nil {
		return fmt.Errorf("cachestore: create temp: %w", err)
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cachestore: write temp: %w", err)
	}
	// fsync the temp file so the rename publishes a durable payload.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cachestore: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cachestore: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("cachestore: rename: %w", err)
	}
	return nil
}
