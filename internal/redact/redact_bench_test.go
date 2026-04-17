package redact

import (
	"regexp"
	"strings"
	"testing"
)

// legacyPatterns and redactLegacy preserve the pre-prefilter implementation so
// the benchmark file documents the before/after delta. Keep in sync with the
// historical init() in redact.go if the regex set ever expands.
var legacyPatterns = func() []*regexp.Regexp {
	raw := []string{
		`AKIA[0-9A-Z]{16}`,
		`(?i)(aws_secret_access_key|aws_secret)\s*[:=]\s*[A-Za-z0-9/+=]{40}`,
		`-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----`,
		`Bearer\s+[A-Za-z0-9\-._~+/]+=*`,
		`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials)\s*[:=]\s*\S+`,
	}
	out := make([]*regexp.Regexp, len(raw))
	for i, r := range raw {
		out[i] = regexp.MustCompile(r)
	}
	return out
}()

func redactLegacy(text string) string {
	for _, p := range legacyPatterns {
		text = p.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

// synthPlan builds a realistic plan-sized payload (~N lines) with a few secrets
// sprinkled in so redaction actually has work to do.
func synthPlan(lines int) string {
	var b strings.Builder
	b.Grow(lines * 80)
	for i := range lines {
		switch i % 50 {
		case 7:
			b.WriteString("config: aws_secret_access_key=AKIAIOSFODNN7EXAMPLEKEYABCDEF0123456789AB\n")
		case 23:
			b.WriteString("headers: Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature==\n")
		case 41:
			b.WriteString("api_key: fake0123456789abcdef0123456789abcdef01\n")
		default:
			b.WriteString("Step N: implement feature X by wiring the service to the controller and update tests.\n")
		}
	}
	return b.String()
}

// BenchmarkRedactLegacy measures the pre-prefilter multi-pass implementation.
func BenchmarkRedactLegacy(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		text := synthPlan(n)
		b.Run("lines="+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = redactLegacy(text)
			}
		})
	}
}

// BenchmarkRedactCurrent measures the live implementation.
func BenchmarkRedactCurrent(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		text := synthPlan(n)
		b.Run("lines="+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = Redact(text)
			}
		})
	}
}

// combinedPattern is a single alternation over all secret patterns so the
// scanner passes the text exactly once.
var combinedPattern = regexp.MustCompile(strings.Join([]string{
	`AKIA[0-9A-Z]{16}`,
	`(?i)(aws_secret_access_key|aws_secret)\s*[:=]\s*[A-Za-z0-9/+=]{40}`,
	`-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----`,
	`Bearer\s+[A-Za-z0-9\-._~+/]+=*`,
	`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials)\s*[:=]\s*\S+`,
}, "|"))

func redactCombined(text string) string {
	return combinedPattern.ReplaceAllString(text, "[REDACTED]")
}

// BenchmarkRedactCombined measures the alternation-based single-pass variant.
func BenchmarkRedactCombined(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		text := synthPlan(n)
		b.Run("lines="+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = redactCombined(text)
			}
		})
	}
}

// patternWithPrefilter pairs a regex with cheap substring checks. If none of
// the markers appear in the text, the regex is skipped entirely.
type patternWithPrefilter struct {
	re      *regexp.Regexp
	markers []string // case-insensitive; caller must pre-lowercase input
}

var prefiltered = []patternWithPrefilter{
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`), []string{"AKIA"}},
	{regexp.MustCompile(`(?i)(aws_secret_access_key|aws_secret)\s*[:=]\s*[A-Za-z0-9/+=]{40}`), []string{"aws_secret"}},
	{regexp.MustCompile(`-----BEGIN [A-Z ]+PRIVATE KEY-----[\s\S]*?-----END [A-Z ]+PRIVATE KEY-----`), []string{"-----BEGIN"}},
	{regexp.MustCompile(`Bearer\s+[A-Za-z0-9\-._~+/]+=*`), []string{"Bearer"}},
	{regexp.MustCompile(`(?i)(api[_-]?key|api[_-]?secret|secret[_-]?key|token|password|passwd|credentials)\s*[:=]\s*\S+`),
		[]string{"api", "secret", "token", "password", "passwd", "credentials"}},
}

func redactPrefiltered(text string) string {
	// AKIA and -----BEGIN are case-sensitive markers; the rest are ASCII and
	// we can do a case-insensitive contains by allocating one lowered copy.
	lower := strings.ToLower(text)
	for _, p := range prefiltered {
		hit := false
		for _, m := range p.markers {
			if m == "AKIA" || strings.HasPrefix(m, "-----") {
				if strings.Contains(text, m) {
					hit = true
					break
				}
			} else if strings.Contains(lower, m) {
				hit = true
				break
			}
		}
		if !hit {
			continue
		}
		text = p.re.ReplaceAllString(text, "[REDACTED]")
	}
	return text
}

func BenchmarkRedactPrefiltered(b *testing.B) {
	for _, n := range []int{100, 1000, 10000} {
		text := synthPlan(n)
		b.Run("lines="+itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(text)))
			for b.Loop() {
				_ = redactPrefiltered(text)
			}
		})
	}
}

// cleanPlan is the common case: plans typically don't contain any secrets.
func cleanPlan(lines int) string {
	var b strings.Builder
	b.Grow(lines * 80)
	for range lines {
		b.WriteString("Step N: implement feature X by wiring the service to the controller and update tests.\n")
	}
	return b.String()
}

func BenchmarkRedactClean_Legacy(b *testing.B) {
	text := cleanPlan(1000)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		_ = redactLegacy(text)
	}
}

func BenchmarkRedactClean_Current(b *testing.B) {
	text := cleanPlan(1000)
	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	for b.Loop() {
		_ = Redact(text)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
