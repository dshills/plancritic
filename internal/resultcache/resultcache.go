// Package resultcache stores completed reviews on disk keyed by a hash
// of everything that determines the LLM's answer (the exact prompt text
// plus provider, model, and sampling settings). A re-run with identical
// inputs is served from disk in milliseconds with zero provider tokens.
//
// Agents iterating on a plan frequently re-run plancritic without having
// changed anything (to "double check", or because a wrapper re-invokes
// the gate after an unrelated step). Those runs are pure waste without
// this cache.
//
// Layout: one JSON file per entry under the cache directory, named by
// the hex key. Entries carry a creation time and expire after the store's
// TTL. Writes are atomic (temp file + rename). Cross-process coordination
// is not attempted: two concurrent runs with the same key both call the
// provider and the later write wins, which is harmless.
package resultcache

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dshills/plancritic/internal/review"
)

const (
	// DefaultTTL is how long a cached review stays valid. Seven days
	// covers a multi-day planning loop without serving results from a
	// model generation that has since moved on.
	DefaultTTL = 7 * 24 * time.Hour

	// EnvDir overrides the cache directory. Tests set it to a temp dir so
	// they never read or write the user's real cache.
	EnvDir = "PLANCRITIC_CACHE_DIR"

	entryVersion = 1
	fileSuffix   = ".json"
)

type entry struct {
	Version   int           `json:"version"`
	CreatedAt time.Time     `json:"created_at"`
	Review    review.Review `json:"review"`
}

// Store is a directory-backed cache of reviews.
type Store struct {
	dir string
	ttl time.Duration
	now func() time.Time
}

// DefaultDir returns the cache directory: $PLANCRITIC_CACHE_DIR/results
// when the variable is set, otherwise <user cache dir>/plancritic/results.
func DefaultDir() (string, error) {
	if d := os.Getenv(EnvDir); d != "" {
		return filepath.Join(d, "results"), nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resultcache: user cache dir: %w", err)
	}
	return filepath.Join(base, "plancritic", "results"), nil
}

// Open returns a store rooted at dir. The directory is created lazily on
// the first Put. A non-positive ttl selects DefaultTTL.
func Open(dir string, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Store{dir: dir, ttl: ttl, now: time.Now}
}

// Key derives a cache key from its parts. Each part is length-prefixed
// before hashing so ("ab","c") and ("a","bc") produce different keys.
func Key(parts ...string) string {
	h := sha256.New()
	var lenBuf [8]byte
	for _, p := range parts {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(p)))
		h.Write(lenBuf[:])
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns the cached review for key. The second result is false on a
// miss, an expired entry, or an unreadable entry; expired and corrupt
// files are removed so they are not re-read on every run.
func (s *Store) Get(key string) (review.Review, bool) {
	path, ok := s.pathFor(key)
	if !ok {
		return review.Review{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return review.Review{}, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil || e.Version != entryVersion {
		_ = os.Remove(path)
		return review.Review{}, false
	}
	if s.expired(e.CreatedAt) {
		_ = os.Remove(path)
		return review.Review{}, false
	}
	return e.Review, true
}

// Put stores rev under key, replacing any existing entry, then prunes
// expired entries on a best-effort basis.
func (s *Store) Put(key string, rev review.Review) error {
	path, ok := s.pathFor(key)
	if !ok {
		return fmt.Errorf("resultcache: invalid key %q", key)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("resultcache: mkdir: %w", err)
	}
	data, err := json.Marshal(entry{Version: entryVersion, CreatedAt: s.now(), Review: rev})
	if err != nil {
		return fmt.Errorf("resultcache: marshal: %w", err)
	}

	tmp, err := os.CreateTemp(s.dir, ".result-*.tmp")
	if err != nil {
		return fmt.Errorf("resultcache: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func(err error) error {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(fmt.Errorf("resultcache: write temp: %w", err))
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(fmt.Errorf("resultcache: sync temp: %w", err))
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("resultcache: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("resultcache: rename: %w", err)
	}

	s.prune()
	return nil
}

// prune removes expired entries and stray temp files. It decides by
// file modification time alone (one stat per file, no reads), because a
// Put always writes the file at the entry's CreatedAt. Get still checks
// the embedded CreatedAt, so an entry whose mtime was disturbed can only
// be pruned late, never served stale. Errors are ignored: pruning is
// housekeeping, never a reason to fail a run.
func (s *Store) prune() {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, de := range dirEntries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		isEntry := strings.HasSuffix(name, fileSuffix)
		isTemp := strings.HasPrefix(name, ".result-") && strings.HasSuffix(name, ".tmp")
		if !isEntry && !isTemp {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		// A temp file older than the TTL is a leftover from a crashed
		// write; a younger one may belong to an in-flight write on
		// another process and is left alone.
		if s.expired(info.ModTime()) {
			_ = os.Remove(filepath.Join(s.dir, name))
		}
	}
}

func (s *Store) expired(t time.Time) bool {
	return !t.Add(s.ttl).After(s.now())
}

// pathFor maps a key to its file path. Keys must be lowercase hex so a
// malformed key can never escape the cache directory.
func (s *Store) pathFor(key string) (string, bool) {
	if len(key) != sha256.Size*2 || !isLowerHex(key) {
		return "", false
	}
	return filepath.Join(s.dir, key+fileSuffix), true
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
