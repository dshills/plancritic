package resultcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dshills/plancritic/internal/review"
)

func sampleReview() review.Review {
	return review.Review{
		Tool: "plancritic",
		Issues: []review.Issue{{
			ID:       "ISSUE-0001",
			Severity: review.SeverityWarn,
			Category: review.CategoryAmbiguity,
			Title:    "t",
			Evidence: []review.Evidence{{Source: "plan", Path: "plan.md", LineStart: 1, LineEnd: 1, Quote: "x"}},
		}},
	}
}

func TestKeyIsStableAndUnambiguous(t *testing.T) {
	first := Key("a", "b")
	second := Key("a", "b")
	if first != second {
		t.Error("same parts must produce the same key")
	}
	if Key("ab", "c") == Key("a", "bc") {
		t.Error("length-prefixing should distinguish differently split parts")
	}
	if Key("a") == Key("a", "") {
		t.Error("an extra empty part should change the key")
	}
	k := Key("x")
	if len(k) != 64 || !isLowerHex(k) {
		t.Errorf("key should be 64 lowercase hex chars, got %q", k)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	s := Open(t.TempDir(), time.Hour)
	key := Key("prompt")
	if _, ok := s.Get(key); ok {
		t.Fatal("expected miss on empty store")
	}
	if err := s.Put(key, sampleReview()); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get(key)
	if !ok {
		t.Fatal("expected hit after Put")
	}
	if len(got.Issues) != 1 || got.Issues[0].ID != "ISSUE-0001" {
		t.Errorf("round-tripped review mismatch: %+v", got)
	}
}

func TestExpiredEntryIsMissAndRemoved(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir, time.Hour)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	key := Key("prompt")
	if err := s.Put(key, sampleReview()); err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, ok := s.Get(key); ok {
		t.Fatal("expected miss after TTL elapsed")
	}
	if _, err := os.Stat(filepath.Join(dir, key+fileSuffix)); !os.IsNotExist(err) {
		t.Errorf("expired entry should have been removed, stat err=%v", err)
	}
}

func TestCorruptEntryIsMissAndRemoved(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir, time.Hour)
	key := Key("prompt")
	path := filepath.Join(dir, key+fileSuffix)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get(key); ok {
		t.Fatal("corrupt entry should be a miss")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("corrupt entry should have been removed, stat err=%v", err)
	}
}

func TestInvalidKeyRejected(t *testing.T) {
	s := Open(t.TempDir(), time.Hour)
	for _, bad := range []string{"", "../../etc/passwd", strings.Repeat("Z", 64), strings.Repeat("a", 63)} {
		if _, ok := s.Get(bad); ok {
			t.Errorf("Get(%q) should miss", bad)
		}
		if err := s.Put(bad, sampleReview()); err == nil {
			t.Errorf("Put(%q) should fail", bad)
		}
	}
}

func TestPutPrunesExpiredSiblings(t *testing.T) {
	dir := t.TempDir()
	s := Open(dir, time.Hour)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	old := Key("old")
	if err := s.Put(old, sampleReview()); err != nil {
		t.Fatal(err)
	}
	// prune decides by file mtime, which Put leaves at wall-clock time;
	// align it with the injected clock so the test is date-independent.
	if err := os.Chtimes(filepath.Join(dir, old+fileSuffix), base, base); err != nil {
		t.Fatal(err)
	}

	s.now = func() time.Time { return base.Add(3 * time.Hour) }
	if err := s.Put(Key("new"), sampleReview()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, old+fileSuffix)); !os.IsNotExist(err) {
		t.Errorf("expired sibling should be pruned on Put, stat err=%v", err)
	}
	if _, ok := s.Get(Key("new")); !ok {
		t.Error("fresh entry should survive prune")
	}
}

func TestDefaultDirHonorsEnv(t *testing.T) {
	t.Setenv(EnvDir, "/tmp/pc-cache-test")
	dir, err := DefaultDir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/tmp/pc-cache-test", "results") {
		t.Errorf("unexpected dir %q", dir)
	}
}

func TestOpenZeroTTLUsesDefault(t *testing.T) {
	s := Open(t.TempDir(), 0)
	if s.ttl != DefaultTTL {
		t.Errorf("ttl = %v, want %v", s.ttl, DefaultTTL)
	}
}
