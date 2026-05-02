package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dshills/plancritic/internal/llm"
	"github.com/dshills/plancritic/internal/profile"
	"github.com/dshills/plancritic/internal/review"
	"github.com/dshills/plancritic/internal/reviewer"
	"github.com/spf13/cobra"
)

type serveFlags struct {
	addr string
	reviewer.Options
}

type reviewRunner func(context.Context, string, reviewer.Options, string) (review.Review, error)

type webServer struct {
	base         reviewer.Options
	runner       reviewRunner
	nonceMu      sync.Mutex
	issuedNonces map[string]time.Time
	lastPrune    time.Time
}

var (
	errCrossOriginRequest = errors.New("cross-origin review requests are not allowed")
	errInvalidFormNonce   = errors.New("invalid form nonce")
	errMissingUpload      = errors.New("missing upload")
)

func newServeCmd() *cobra.Command {
	f := &serveFlags{}
	f.addr = serveEnvStr("PLANCRITIC_ADDR", "127.0.0.1:8080")
	f.Format = "json"
	f.ProfileName = serveEnvStr("PLANCRITIC_PROFILE", "general")
	f.ProviderName = serveEnvStr("PLANCRITIC_PROVIDER", "")
	f.Model = serveEnvStr("PLANCRITIC_MODEL", "")
	f.MaxTokens = serveEnvInt("PLANCRITIC_MAX_TOKENS", 4096)
	f.MaxIssues = serveEnvInt("PLANCRITIC_MAX_ISSUES", 50)
	f.MaxQuestions = serveEnvInt("PLANCRITIC_MAX_QUESTIONS", 20)
	f.MaxInputTokens = serveEnvInt("PLANCRITIC_MAX_INPUT_TOKENS", 0)
	f.Timeout = serveEnvStr("PLANCRITIC_TIMEOUT", "5m")
	f.Temperature = serveEnvFloat("PLANCRITIC_TEMPERATURE", 0.2)
	f.SeverityThreshold = serveEnvStr("PLANCRITIC_SEVERITY_THRESHOLD", "info")
	f.RedactEnabled = serveEnvBool("PLANCRITIC_REDACT", true)
	f.NoCache = serveEnvBool("PLANCRITIC_NO_CACHE", false)
	f.CacheTTL = serveEnvStr("PLANCRITIC_CACHE_TTL", "1h")

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the PlanCritic HTMX web UI",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := &webServer{base: f.Options, runner: reviewer.Run}
			mux := srv.routes()
			writeTimeout := reviewWriteTimeout(f.Timeout)
			log.Printf("plancritic web UI listening on http://%s", f.addr)
			httpSrv := &http.Server{
				Addr:              f.addr,
				Handler:           mux,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       2 * time.Minute,
				WriteTimeout:      writeTimeout,
				IdleTimeout:       2 * time.Minute,
			}
			return httpSrv.ListenAndServe()
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&f.addr, "addr", f.addr, "HTTP listen address")
	flags.StringVar(&f.ProviderName, "provider", f.ProviderName, "LLM provider: anthropic, openai, or gemini")
	flags.StringVar(&f.Model, "model", f.Model, "Model ID (e.g., claude-sonnet-4-6, gpt-5.2)")
	flags.StringVar(&f.ProfileName, "profile", f.ProfileName, "Default profile name")
	flags.StringVar(&f.SeverityThreshold, "severity-threshold", f.SeverityThreshold, "Default minimum severity: info, warn, or critical")
	flags.BoolVar(&f.Strict, "strict", f.Strict, "Enable strict grounding mode by default")
	flags.IntVar(&f.MaxTokens, "max-tokens", f.MaxTokens, "Max response tokens")
	flags.IntVar(&f.MaxIssues, "max-issues", f.MaxIssues, "Max issues to return")
	flags.IntVar(&f.MaxQuestions, "max-questions", f.MaxQuestions, "Max questions to return")
	flags.IntVar(&f.MaxInputTokens, "max-input-tokens", f.MaxInputTokens, "Max estimated input tokens (0=unlimited)")
	flags.StringVar(&f.Timeout, "timeout", f.Timeout, "HTTP timeout for LLM requests")
	flags.Float64Var(&f.Temperature, "temperature", f.Temperature, "Model temperature")
	flags.BoolVar(&f.RedactEnabled, "redact", f.RedactEnabled, "Redact secrets before sending to model")
	flags.BoolVar(&f.NoCache, "no-cache", f.NoCache, "Disable prompt caching")
	flags.StringVar(&f.CacheTTL, "cache-ttl", f.CacheTTL, "TTL for provider-side context caches")
	flags.BoolVar(&f.Verbose, "verbose", false, "Print review progress to stderr")

	return cmd
}

func (s *webServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/favicon.svg", s.favicon)
	mux.HandleFunc("/models", s.models)
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/check", s.check)
	return mux
}

func (s *webServer) favicon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, faviconSVG)
}

type modelsResponse struct {
	Provider string          `json:"provider"`
	Models   []llm.ModelInfo `json:"models"`
}

func (s *webServer) models(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOriginRequest(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("provider")))
	if provider == "" {
		provider = s.base.ProviderName
	}
	if provider == "" {
		provider = "openai"
	}
	if !llm.IsSupportedProvider(provider) {
		http.Error(w, fmt.Sprintf("invalid provider %q", provider), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	models, err := llm.ListModels(ctx, provider)
	if err != nil {
		log.Printf("plancritic web list models failed: %v", err)
		http.Error(w, "Unable to load provider models.", http.StatusBadGateway)
		return
	}
	if models == nil {
		models = []llm.ModelInfo{}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(modelsResponse{
		Provider: provider,
		Models:   models,
	}); err != nil {
		log.Printf("plancritic web write models response: %v", err)
	}
}

func (s *webServer) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	profiles, err := profile.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	formNonce, err := s.issueFormNonce()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := pageData{
		Profiles:            profiles,
		DefaultProfile:      s.base.ProfileName,
		DefaultProvider:     s.base.ProviderName,
		DefaultModel:        s.base.Model,
		DefaultSeverity:     s.base.SeverityThreshold,
		DefaultStrict:       s.base.Strict,
		DefaultRedact:       s.base.RedactEnabled,
		DefaultNoCache:      s.base.NoCache,
		DefaultMaxIssues:    s.base.MaxIssues,
		DefaultMaxQuestions: s.base.MaxQuestions,
		FormNonce:           formNonce,
	}
	if data.DefaultProvider == "" {
		data.DefaultProvider = "openai"
	}
	if data.DefaultSeverity == "" {
		data.DefaultSeverity = "info"
	}
	if data.DefaultMaxIssues == 0 {
		data.DefaultMaxIssues = review.DefaultMaxIssues
	}
	if data.DefaultMaxQuestions == 0 {
		data.DefaultMaxQuestions = review.DefaultMaxQuestions
	}
	executeTemplate(w, pageHTML, data)
}

func (s *webServer) check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	start := time.Now()
	if !sameOriginRequest(r) {
		renderError(w, errCrossOriginRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadMemory); err != nil {
		renderError(w, fmt.Errorf("failed to parse form: %w", err))
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	formNonce := r.FormValue("form_nonce")
	if !s.validFormNonce(r, formNonce) {
		renderError(w, errInvalidFormNonce)
		return
	}
	nextNonce, err := s.issueFormNonce()
	if err != nil {
		renderError(w, err)
		return
	}
	fail := func(err error) {
		renderErrorWithNonce(w, err, nextNonce)
	}

	dir, err := os.MkdirTemp("", "plancritic-web-*")
	if err != nil {
		fail(err)
		return
	}
	defer os.RemoveAll(dir)

	planPath, planName, err := saveUploadedFile(r.MultipartForm, "plan", dir)
	if err != nil {
		fail(err)
		return
	}
	contextPaths, err := saveUploadedFiles(r.MultipartForm, "context", dir)
	if err != nil {
		fail(err)
		return
	}

	f := s.flagsFromForm(r, contextPaths)
	rev, err := s.runner(r.Context(), planPath, f, version)
	if err != nil {
		fail(err)
		return
	}

	planLines, err := displayPlanLines(planPath)
	if err != nil {
		fail(err)
		return
	}
	findings := findingsFromReview(rev, f.SeverityThreshold)
	addPlanLineBadges(planLines, findings, planName, filepath.Base(planPath))
	data := resultData{
		Review:     rev,
		PlanName:   planName,
		PlanLines:  planLines,
		Elapsed:    time.Since(start).Round(100 * time.Millisecond).String(),
		Findings:   findings,
		ModelLabel: rev.Meta.Model,
		FormNonce:  nextNonce,
	}
	executeTemplate(w, resultHTML, data)
}

func (s *webServer) flagsFromForm(r *http.Request, contextPaths []string) reviewer.Options {
	f := s.base
	f.Format = "json"
	f.Out = ""
	f.ContextPaths = contextPaths
	f.PatchOut = ""
	f.FailOn = ""
	f.Debug = false
	f.Provider = nil
	f.ProfileName = formValue(r, "profile", f.ProfileName)
	f.ProviderName = formValue(r, "provider", f.ProviderName)
	f.Model = formValue(r, "model", f.Model)
	f.SeverityThreshold = formValue(r, "severity", f.SeverityThreshold)
	f.Strict = r.FormValue("strict") == "on"
	f.RedactEnabled = r.FormValue("redact") == "on"
	f.NoCache = r.FormValue("no_cache") == "on"
	f.MaxIssues = formInt(r, "max_issues", f.MaxIssues)
	f.MaxQuestions = formInt(r, "max_questions", f.MaxQuestions)
	if !builtinProfileExists(f.ProfileName) {
		f.ProfileName = s.base.ProfileName
	}
	return f
}

func formValue(r *http.Request, key, fallback string) string {
	v := strings.TrimSpace(r.FormValue(key))
	if v == "" {
		return fallback
	}
	return v
}

func formInt(r *http.Request, key string, fallback int) int {
	v := strings.TrimSpace(r.FormValue(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func builtinProfileExists(name string) bool {
	profiles, err := profile.List()
	if err != nil {
		return false
	}
	for _, p := range profiles {
		if p == name {
			return true
		}
	}
	return false
}

func serveEnvStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func serveEnvBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func serveEnvInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func serveEnvFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return f
}

func reviewWriteTimeout(timeout string) time.Duration {
	d, err := time.ParseDuration(timeout)
	if err != nil || d <= 0 {
		return 6 * time.Minute
	}
	d += time.Minute
	if d > 10*time.Minute {
		return 10 * time.Minute
	}
	return d
}

func newFormNonce() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate form nonce: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func (s *webServer) issueFormNonce() (string, error) {
	nonce, err := newFormNonce()
	if err != nil {
		return "", err
	}
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	if s.issuedNonces == nil {
		s.issuedNonces = make(map[string]time.Time)
	}
	now := time.Now()
	s.pruneExpiredNoncesLocked(now, false)
	if len(s.issuedNonces) >= maxIssuedNonces {
		s.evictOldestNonceLocked()
	}
	s.issuedNonces[nonce] = now.Add(formNonceTTL)
	return nonce, nil
}

func (s *webServer) validFormNonce(r *http.Request, got string) bool {
	if got == "" {
		return false
	}
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	now := time.Now()
	s.pruneExpiredNoncesLocked(now, false)
	expiresAt, ok := s.issuedNonces[got]
	if !ok || now.After(expiresAt) {
		return false
	}
	delete(s.issuedNonces, got)
	return true
}

func (s *webServer) pruneExpiredNoncesLocked(now time.Time, force bool) {
	if !force && now.Sub(s.lastPrune) < formNoncePruneInterval {
		return
	}
	s.lastPrune = now
	for nonce, expiresAt := range s.issuedNonces {
		if now.After(expiresAt) {
			delete(s.issuedNonces, nonce)
		}
	}
}

func (s *webServer) evictOldestNonceLocked() {
	var oldestNonce string
	var oldestExpiry time.Time
	for nonce, expiresAt := range s.issuedNonces {
		if oldestNonce == "" || expiresAt.Before(oldestExpiry) {
			oldestNonce = nonce
			oldestExpiry = expiresAt
		}
	}
	if oldestNonce != "" {
		delete(s.issuedNonces, oldestNonce)
	}
}

func sameOriginRequest(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return requestHeaderMatchesHost(origin, r.Host)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return requestHeaderMatchesHost(referer, r.Host)
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func requestHeaderMatchesHost(raw, host string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host) || (isLoopbackHost(u.Hostname()) && isLoopbackHost(hostWithoutPort(host)))
}

func hostWithoutPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sanitizeUploadName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	cleaned := strings.Trim(b.String(), "._")
	if cleaned == "" {
		return "upload"
	}
	return cleaned
}

func saveUploadedFile(form *multipart.Form, field, dir string) (string, string, error) {
	files := form.File[field]
	if len(files) == 0 || files[0].Filename == "" {
		return "", "", fmt.Errorf("%w: %s file", errMissingUpload, field)
	}
	path, err := saveFileHeader(files[0], dir, "plan")
	if err != nil {
		return "", "", err
	}
	return path, files[0].Filename, nil
}

func saveUploadedFiles(form *multipart.Form, field, dir string) ([]string, error) {
	if len(form.File[field]) > maxContextFiles {
		return nil, fmt.Errorf("too many context files: max %d", maxContextFiles)
	}
	var paths []string
	for i, fh := range form.File[field] {
		if fh.Filename == "" {
			continue
		}
		path, err := saveFileHeader(fh, dir, fmt.Sprintf("%s-%d", field, i+1))
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func saveFileHeader(fh *multipart.FileHeader, dir, prefix string) (path string, err error) {
	src, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()

	name := sanitizeUploadName(fh.Filename)
	if name == "." || name == string(filepath.Separator) {
		return "", fmt.Errorf("invalid upload filename %q", fh.Filename)
	}
	path = filepath.Join(dir, prefix+"-"+name)
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() {
		closeErr := dst.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if _, err = io.Copy(dst, src); err != nil {
		return "", err
	}
	return path, nil
}

type pageData struct {
	Profiles            []string
	DefaultProfile      string
	DefaultProvider     string
	DefaultModel        string
	DefaultSeverity     string
	DefaultStrict       bool
	DefaultRedact       bool
	DefaultNoCache      bool
	DefaultMaxIssues    int
	DefaultMaxQuestions int
	FormNonce           string
}

type resultData struct {
	Review     review.Review
	PlanName   string
	PlanLines  []numberedLine
	Elapsed    string
	Findings   []findingRow
	ModelLabel string
	FormNonce  string
}

type numberedLine struct {
	Number int
	Text   string
	Badges []lineBadge
}

type lineBadge struct {
	DOMID         string
	Label         string
	SeverityClass string
}

type findingRow struct {
	Kind          string
	ID            string
	DOMID         string
	Severity      review.Severity
	SeverityClass string
	Category      string
	Title         string
	Detail        []string
	Evidence      []review.Evidence
}

func displayPlanLines(path string) ([]numberedLine, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 4096), maxPreviewLineBytes)
	lines := make([]numberedLine, 0, 128)
	bytesRead := 0
	truncated := false
	for scanner.Scan() {
		line := scanner.Text()
		bytesRead += len(line) + 1
		if bytesRead > maxPreviewBytes {
			allowed := len(line) - (bytesRead - maxPreviewBytes)
			if allowed > 0 {
				for allowed > 0 && !utf8.ValidString(line[:allowed]) {
					allowed--
				}
				line = line[:allowed]
				lines = append(lines, numberedLine{Number: len(lines) + 1, Text: line})
			}
			truncated = true
			break
		}
		lines = append(lines, numberedLine{Number: len(lines) + 1, Text: line})
		if len(lines) == maxPreviewLines {
			truncated = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		truncated = true
	}
	if truncated {
		lines = append(lines, numberedLine{Number: len(lines) + 1, Text: "[preview truncated]"})
	}
	return lines, nil
}

func findingsFromReview(rev review.Review, threshold string) []findingRow {
	rows := make([]findingRow, 0, len(rev.Issues)+len(rev.Questions))
	normalizedThreshold := strings.ToLower(threshold)
	for _, issue := range rev.Issues {
		if meetsSeverityThreshold(issue.Severity, normalizedThreshold) {
			rows = append(rows, findingRow{
				Kind:          "ISSUE",
				ID:            issue.ID,
				DOMID:         domID("issue", issue.ID),
				Severity:      issue.Severity,
				SeverityClass: strings.ToUpper(string(issue.Severity)),
				Category:      string(issue.Category),
				Title:         issue.Title,
				Detail:        nonEmptyStrings(issue.Description, issue.Impact, issue.Recommendation),
				Evidence:      issue.Evidence,
			})
		}
	}
	for _, question := range rev.Questions {
		if meetsSeverityThreshold(question.Severity, normalizedThreshold) {
			rows = append(rows, findingRow{
				Kind:          "QUESTION",
				ID:            question.ID,
				DOMID:         domID("question", question.ID),
				Severity:      question.Severity,
				SeverityClass: strings.ToUpper(string(question.Severity)),
				Category:      "QUESTION",
				Title:         question.Question,
				Detail:        questionDetail(question),
				Evidence:      question.Evidence,
			})
		}
	}
	return rows
}

func questionDetail(question review.Question) []string {
	values := make([]string, 0, 1+len(question.SuggestedAnswers))
	values = append(values, question.WhyNeeded)
	values = append(values, question.SuggestedAnswers...)
	return nonEmptyStrings(values...)
}

func addPlanLineBadges(lines []numberedLine, findings []findingRow, planNames ...string) {
	if len(lines) == 0 {
		return
	}
	maxLine := lines[len(lines)-1].Number
	byLine := make(map[int][]lineBadge)
	seen := make(map[int]map[string]bool)
	for _, finding := range findings {
		for _, ev := range finding.Evidence {
			if ev.Source != "plan" {
				continue
			}
			if !matchesPlanEvidencePath(ev.Path, planNames...) {
				continue
			}
			start := ev.LineStart
			if start < 1 {
				start = 1
			}
			if start > maxLine {
				continue
			}
			end := ev.LineEnd
			if end < start {
				end = start
			}
			if end > maxLine {
				end = maxLine
			}
			for n := start; n <= end; n++ {
				if seen[n] == nil {
					seen[n] = make(map[string]bool)
				}
				if seen[n][finding.DOMID] {
					continue
				}
				seen[n][finding.DOMID] = true
				byLine[n] = append(byLine[n], lineBadge{
					DOMID:         finding.DOMID,
					Label:         strings.TrimSpace(string(finding.Severity) + " " + finding.ID),
					SeverityClass: finding.SeverityClass,
				})
			}
		}
	}
	for i := range lines {
		lines[i].Badges = byLine[lines[i].Number]
	}
}

func matchesPlanEvidencePath(path string, planNames ...string) bool {
	if path == "" {
		return true
	}
	base := filepath.Base(path)
	for _, name := range planNames {
		if name != "" && strings.EqualFold(base, filepath.Base(name)) {
			return true
		}
	}
	return false
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func domID(prefix, id string) string {
	var b strings.Builder
	b.WriteString(prefix)
	b.WriteByte('-')
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	b.WriteString("-")
	b.WriteString(strconv.FormatUint(uint64(h.Sum32()), 16))
	return b.String()
}

func meetsSeverityThreshold(severity review.Severity, threshold string) bool {
	switch threshold {
	case "critical":
		return severity == review.SeverityCritical
	case "warn":
		return severity == review.SeverityCritical || severity == review.SeverityWarn
	default:
		return true
	}
}

func renderError(w http.ResponseWriter, err error) {
	log.Printf("plancritic web error: %v", err)
	if tmplErr := writeTemplate(w, errorHTML, publicErrorMessage(err), statusForError(err)); tmplErr != nil {
		http.Error(w, tmplErr.Error(), http.StatusInternalServerError)
	}
}

func renderErrorWithNonce(w http.ResponseWriter, err error, nonce string) {
	log.Printf("plancritic web error: %v", err)
	data := errorData{Message: publicErrorMessage(err), FormNonce: nonce}
	if tmplErr := writeTemplate(w, errorWithNonceHTML, data, statusForError(err)); tmplErr != nil {
		http.Error(w, tmplErr.Error(), http.StatusInternalServerError)
	}
}

type errorData struct {
	Message   string
	FormNonce string
}

func publicErrorMessage(err error) string {
	switch {
	case errors.Is(err, errCrossOriginRequest):
		return "Cross-origin review requests are not allowed."
	case errors.Is(err, errInvalidFormNonce):
		return "The review form expired. Reload the page and try again."
	case errors.Is(err, errMissingUpload):
		return "Missing plan file."
	}
	var ee *reviewer.Error
	if errors.As(err, &ee) {
		switch ee.Code {
		case 3, 5:
			return "Input error. Check the uploaded files and selected options."
		case 4:
			return "Model/provider error. Check the server logs."
		}
	}
	return "Unexpected server error. Check the server logs."
}

func statusForError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var ee *reviewer.Error
	if errors.As(err, &ee) {
		switch ee.Code {
		case 3, 5:
			return http.StatusBadRequest
		case 4:
			return http.StatusBadGateway
		default:
			return http.StatusInternalServerError
		}
	}
	if errors.Is(err, errCrossOriginRequest) {
		return http.StatusForbidden
	}
	if errors.Is(err, errInvalidFormNonce) {
		return http.StatusForbidden
	}
	if errors.Is(err, errMissingUpload) || strings.Contains(err.Error(), "failed to parse form") {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

func executeTemplate(w http.ResponseWriter, tmpl *template.Template, data any) {
	if err := writeTemplate(w, tmpl, data, http.StatusOK); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func writeTemplate(w http.ResponseWriter, tmpl *template.Template, data any, status int) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		log.Printf("plancritic web write error: %v", err)
	}
	return nil
}

const (
	maxUploadBytes         = 16 << 20
	maxUploadMemory        = 8 << 20
	maxContextFiles        = 20
	maxPreviewBytes        = 512 * 1024
	maxPreviewLineBytes    = 1024 * 1024
	maxPreviewLines        = 2000
	formNonceTTL           = 30 * time.Minute
	formNoncePruneInterval = time.Minute
	maxIssuedNonces        = 1024
)

const faviconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64">
  <rect width="64" height="64" rx="14" fill="#111827"/>
  <path d="M19 12h20l8 8v32H19z" fill="#f8fafc"/>
  <path d="M39 12v9h8" fill="#dbeafe"/>
  <path d="M25 29h17M25 36h12" stroke="#64748b" stroke-width="4" stroke-linecap="round"/>
  <path d="M24 48l7-7 5 5 11-14" fill="none" stroke="#2563eb" stroke-width="5" stroke-linecap="round" stroke-linejoin="round"/>
</svg>`

const pageTemplate = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>PlanCritic</title>
  <link rel="icon" href="/favicon.svg" type="image/svg+xml">
  <script src="https://unpkg.com/htmx.org@1.9.12" integrity="sha384-ujb1lZYygJmzgSwoxRggbCHcjc0rB2XoQrxeTUQyRjrOnlCoYta87iKBWq3EsdM2" crossorigin="anonymous"></script>
  <script>
    document.addEventListener("htmx:beforeSwap", function (event) {
      if (event.detail.xhr.status >= 400) {
        event.detail.shouldSwap = true;
        event.detail.isError = false;
      }
    });

    (function () {
      var timerID = 0;
      var startedAt = 0;
      var lastModalOpener = null;

      function formFromEvent(event) {
        var elt = event.detail && event.detail.elt;
        if (!elt || !elt.matches || !elt.matches("[data-review-form]")) {
          return null;
        }
        return elt;
      }

      function elapsed() {
        return ((performance.now() - startedAt) / 1000).toFixed(1) + "s";
      }

      function renderPending() {
        var results = document.getElementById("results");
        if (!results) {
          return;
        }
        results.innerHTML = '<div class="pending-status"><span class="spinner" aria-hidden="true"></span><span>Checking&nbsp;&nbsp;<span id="elapsed_time">' + elapsed() + '</span></span></div>';
      }

      function startPending(form) {
        var button = form.querySelector("[data-check-button]");
        if (button) {
          button.dataset.originalText = button.textContent;
          button.textContent = "Checking...";
          button.disabled = true;
        }
        startedAt = performance.now();
        renderPending();
        window.clearInterval(timerID);
        timerID = window.setInterval(renderPending, 100);
      }

      function stopPending(form) {
        window.clearInterval(timerID);
        timerID = 0;
        var button = form.querySelector("[data-check-button]");
        if (button) {
          button.disabled = false;
          button.textContent = button.dataset.originalText || "Check plan";
        }
      }

      function initializeModelPicker() {
        var form = document.querySelector("[data-review-form]");
        var provider = form ? form.querySelector('select[name="provider"]') : null;
        var model = form ? form.querySelector('input[name="model"]') : null;
        if (!form || !provider || !model) {
          return;
        }
        loadProviderModels(form);
      }

      function loadProviderModels(form) {
        var provider = form.querySelector('select[name="provider"]');
        var model = form.querySelector('input[name="model"]');
        var status = form.querySelector("#model_picker_status");
        if (!provider || !model) {
          return;
        }
        var requestID = String(Date.now()) + ":" + provider.value;
        form.dataset.modelRequestId = requestID;
        if (form.modelRequestController) {
          form.modelRequestController.abort();
        }
        var controller = window.AbortController ? new AbortController() : null;
        form.modelRequestController = controller;
        if (status) {
          status.textContent = "Loading available models...";
        }
        var options = {
          method: "GET",
          credentials: "same-origin",
          headers: { "Accept": "application/json" }
        };
        if (controller) {
          options.signal = controller.signal;
        }
        fetch("/models?provider=" + encodeURIComponent(provider.value), options).then(function (response) {
          if (!response.ok) {
            return response.text().then(function (text) {
              throw new Error(text || response.statusText || "Unable to load provider models.");
            });
          }
          return response.json();
        }).then(function (payload) {
          if (form.dataset.modelRequestId !== requestID) {
            return;
          }
          var models = Array.isArray(payload.models) ? payload.models : [];
          form.modelOptions = models;
          if (status) {
            status.textContent = models.length ? models.length + " models loaded." : "No models returned; enter a model manually.";
          }
        }).catch(function (error) {
          if (error && error.name === "AbortError") {
            return;
          }
          if (form.dataset.modelRequestId !== requestID) {
            return;
          }
          form.modelOptions = [];
          if (status) {
            status.textContent = "Could not load models; enter a model manually.";
          }
        });
      }

      function showModelMenu(form, filter) {
        var menu = form ? form.querySelector("#model_options") : null;
        var model = form ? form.querySelector('input[name="model"]') : null;
        if (!menu || !model) {
          return;
        }
        var models = Array.isArray(form.modelOptions) ? form.modelOptions : [];
        var needle = (filter || "").trim().toLowerCase();
        var visible = models.filter(function (item) {
          if (!item || !item.id) {
            return false;
          }
          if (!needle) {
            return true;
          }
          return item.id.toLowerCase().indexOf(needle) >= 0 ||
            (item.display_name || "").toLowerCase().indexOf(needle) >= 0;
        });
        menu.replaceChildren();
        visible.forEach(function (item) {
          var button = document.createElement("button");
          button.type = "button";
          button.className = "model-option";
          button.setAttribute("role", "option");
          button.setAttribute("data-model-option", item.id);
          var id = document.createElement("span");
          id.textContent = item.id;
          button.append(id);
          if (item.display_name) {
            var name = document.createElement("small");
            name.textContent = item.display_name;
            button.append(name);
          }
          menu.append(button);
        });
        menu.hidden = visible.length === 0;
        model.setAttribute("aria-expanded", menu.hidden ? "false" : "true");
      }

      function hideModelMenu(form) {
        var menu = form ? form.querySelector("#model_options") : null;
        var model = form ? form.querySelector('input[name="model"]') : null;
        if (menu) {
          menu.hidden = true;
        }
        if (model) {
          model.setAttribute("aria-expanded", "false");
        }
      }

      document.addEventListener("htmx:beforeRequest", function (event) {
        var form = formFromEvent(event);
        if (form) {
          startPending(form);
        }
      });

      document.addEventListener("htmx:afterRequest", function (event) {
        var form = formFromEvent(event);
        if (form) {
          stopPending(form);
        }
      });

      document.addEventListener("click", function (event) {
        var modelOption = event.target.closest("[data-model-option]");
        if (modelOption) {
          var form = modelOption.closest("form");
          var model = form ? form.querySelector('input[name="model"]') : null;
          if (model) {
            model.value = modelOption.getAttribute("data-model-option") || "";
            model.focus();
          }
          hideModelMenu(form);
          return;
        }
        var opener = event.target.closest("[data-modal-target]");
        if (opener) {
          var modal = document.getElementById(opener.getAttribute("data-modal-target"));
          if (modal) {
            lastModalOpener = opener;
            modal.hidden = false;
            var close = modal.querySelector("[data-modal-close]");
            if (close) {
              close.focus();
            }
          }
          return;
        }
        var closer = event.target.closest("[data-modal-close]");
        var backdrop = event.target.classList && event.target.classList.contains("modal") ? event.target : null;
        if (closer || backdrop) {
          var active = backdrop || closer.closest(".modal");
          if (active) {
            active.hidden = true;
            if (lastModalOpener) {
              lastModalOpener.focus();
            }
          }
        }
        if (!event.target.closest || !event.target.closest(".model-picker")) {
          document.querySelectorAll("[data-review-form]").forEach(hideModelMenu);
        }
      });

      document.addEventListener("change", function (event) {
        var provider = event.target;
        if (!provider || !provider.matches || !provider.matches('select[name="provider"]')) {
          return;
        }
        var form = provider.form;
        var model = form ? form.querySelector('input[name="model"]') : null;
        if (!model) {
          return;
        }
        model.value = "";
        loadProviderModels(form);
        showModelMenu(form, "");
      });

      document.addEventListener("focusin", function (event) {
        var model = event.target;
        if (!model || !model.matches || !model.matches('input[name="model"]')) {
          return;
        }
        showModelMenu(model.form, "");
      });

      document.addEventListener("input", function (event) {
        var model = event.target;
        if (!model || !model.matches || !model.matches('input[name="model"]')) {
          return;
        }
        showModelMenu(model.form, model.value);
      });

      document.addEventListener("mousedown", function (event) {
        var option = event.target.closest ? event.target.closest("[data-model-option]") : null;
        if (option) {
          event.preventDefault();
        }
      });

      document.addEventListener("keydown", function (event) {
        if (event.key !== "Escape") {
          return;
        }
        document.querySelectorAll(".modal:not([hidden])").forEach(function (modal) {
          modal.hidden = true;
        });
        if (lastModalOpener) {
          lastModalOpener.focus();
        }
      });
      if (document.readyState === "loading") {
        document.addEventListener("DOMContentLoaded", initializeModelPicker);
      } else {
        initializeModelPicker();
      }
    })();
  </script>
  <style>
    :root { color-scheme: light; --line:#cbd5e1; --muted:#64748b; --text:#111827; --panel:#f8fafc; --blue:#2563eb; --red:#dc2626; --amber:#d97706; --green:#16a34a; }
    * { box-sizing: border-box; }
    body { margin:0; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; color:var(--text); background:#eef3f8; }
    .shell { display:grid; grid-template-columns: 360px minmax(0, 1fr); min-height:100vh; }
    aside { background:#f8fafc; border-right:1px solid var(--line); padding:22px 18px; }
    main { padding:24px 32px; }
    h1 { margin:0; font-size:28px; line-height:1.1; }
    h2 { margin:0 0 16px; font-size:20px; }
    label { display:block; margin:16px 0 7px; font-size:13px; font-weight:700; color:#334155; }
    input, select { width:100%; min-height:42px; border:1px solid #b8c6d8; border-radius:7px; padding:9px 12px; background:white; color:var(--text); font:inherit; }
    input[type=checkbox] { width:16px; min-height:16px; margin:0 10px 0 4px; }
    .brand { padding-bottom:22px; border-bottom:1px solid var(--line); margin-bottom:26px; }
    .sub { color:var(--muted); margin-top:5px; }
    .group { border:1px solid var(--line); border-radius:7px; padding:14px 12px 16px; background:#f8fafc; margin-bottom:22px; }
    .model-picker { position:relative; }
    .model-menu { position:absolute; left:12px; right:12px; top:calc(100% - 18px); z-index:20; max-height:260px; overflow:auto; border:1px solid #b8c6d8; border-radius:7px; background:white; box-shadow:0 14px 36px rgba(15,23,42,.16); padding:6px; }
    .model-menu[hidden] { display:none; }
    .model-option { display:block; width:100%; min-height:0; margin:0; border:0; border-radius:5px; background:white; color:var(--text); padding:8px 10px; text-align:left; cursor:pointer; font-weight:700; }
    .model-option:hover { background:#eff6ff; }
    .model-option small { display:block; margin-top:2px; color:#64748b; font-weight:600; }
    .field-status { min-height:16px; margin:6px 0 0; color:#52627a; font-size:12px; }
    .row { display:flex; align-items:center; gap:8px; margin-top:16px; font-weight:700; font-size:13px; color:#334155; }
    .twocol { display:grid; grid-template-columns: 1fr 1fr; gap:12px; }
    button { width:100%; border:0; border-radius:7px; min-height:44px; padding:10px 14px; margin-top:22px; background:var(--blue); color:white; font-weight:800; font:inherit; cursor:pointer; }
    button:hover { background:#1d4ed8; }
    button:disabled { background:#6b8fe8; cursor:not-allowed; }
    .hint { color:#52627a; font-size:12px; margin-top:7px; }
    .placeholder { border:1px dashed #aab8cc; border-radius:7px; background:#f8fafc; color:#475569; padding:24px; }
    .status { border:1px solid #86efac; background:#ecfdf5; color:#14532d; border-radius:7px; padding:13px 14px; margin-bottom:18px; }
    .pending-status { border:1px solid #93c5fd; background:#eff6ff; color:#172554; border-radius:7px; padding:13px 14px; margin-bottom:18px; display:flex; align-items:center; gap:10px; }
    .spinner { width:16px; height:16px; border-radius:999px; border:2px solid #bfdbfe; border-top-color:#3b82f6; animation:spin .8s linear infinite; flex:0 0 auto; }
    @keyframes spin { to { transform:rotate(360deg); } }
    .error { border-color:#fecaca; background:#fef2f2; color:#991b1b; }
    .card { border:1px solid var(--line); border-radius:7px; background:#fff; padding:22px; margin-bottom:24px; box-shadow:0 8px 20px rgba(15,23,42,.05); }
    .summary-head { display:flex; justify-content:space-between; gap:16px; align-items:flex-start; margin-bottom:16px; }
    .verdict { font-size:30px; font-weight:900; letter-spacing:0; }
    .meta { display:flex; gap:10px; flex-wrap:wrap; }
    .pill { border:1px solid var(--line); border-radius:7px; padding:9px 12px; background:#f8fafc; min-width:110px; }
    .pill b { display:block; font-size:11px; color:#64748b; text-transform:uppercase; }
    .metrics { display:grid; grid-template-columns: repeat(4, minmax(0,1fr)); gap:12px; }
    .metric { border:1px solid var(--line); border-radius:7px; padding:14px; background:#f8fafc; }
    .metric b { display:block; color:#64748b; text-transform:uppercase; font-size:12px; }
    .metric span { display:block; font-size:30px; font-weight:900; margin-top:4px; }
    .critical { border-color:#fecaca; background:#fff1f2; }
    .warn { border-color:#fde68a; background:#fffbeb; }
    .info { border-color:#bfdbfe; background:#eff6ff; }
    .finding { width:100%; display:grid; grid-template-columns: 82px 92px minmax(0,1fr); gap:12px; align-items:center; border:1px solid var(--line); border-left:4px solid #94a3b8; border-radius:7px; padding:10px 12px; margin-top:8px; background:#f8fafc; color:var(--text); text-align:left; font-weight:400; cursor:pointer; }
    .finding:hover { background:#f1f5f9; }
    .finding.CRITICAL { border-left-color:#ef4444; }
    .finding.WARN { border-left-color:#f59e0b; }
    .finding.INFO { border-left-color:#3b82f6; }
    .badge { justify-self:start; border-radius:999px; padding:4px 9px; font-size:11px; font-weight:900; background:#e2e8f0; }
    .badge.CRITICAL { color:#991b1b; background:#fee2e2; }
    .badge.WARN { color:#92400e; background:#fef3c7; }
    .badge.INFO { color:#1e3a8a; background:#dbeafe; }
    .id { color:#475569; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size:13px; }
    .title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .source { border:1px solid var(--line); border-radius:7px; overflow:hidden; background:white; }
    .line { display:grid; grid-template-columns:64px minmax(0,1fr); border-bottom:1px solid #e5e7eb; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size:13px; }
    .line:last-child { border-bottom:0; }
    .line.with-badges { background:#fffbeb; }
    .num { background:#e9eff6; color:#475569; text-align:right; padding:7px 10px; user-select:none; }
    .code { display:flex; justify-content:space-between; gap:16px; padding:7px 12px; white-space:pre-wrap; overflow-wrap:anywhere; }
    .line-text { min-width:0; }
    .line-badges { display:flex; flex-wrap:wrap; justify-content:flex-end; gap:6px; align-self:flex-start; font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; }
    .line-badge { border:0; border-radius:999px; margin:0; min-height:0; width:auto; padding:4px 9px; background:#fef3c7; color:#92400e; font-size:11px; font-weight:900; cursor:pointer; }
    .line-badge.CRITICAL { color:#991b1b; background:#fee2e2; }
    .line-badge.INFO { color:#1e3a8a; background:#dbeafe; }
    .modal[hidden] { display:none; }
    .modal { position:fixed; inset:0; z-index:50; display:flex; align-items:center; justify-content:center; padding:24px; background:rgba(15,23,42,.52); }
    .modal-box { width:min(720px, 100%); max-height:min(80vh, 760px); overflow:auto; border-radius:7px; background:white; padding:24px; box-shadow:0 22px 70px rgba(15,23,42,.28); }
    .modal-head { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; margin-bottom:12px; }
    .modal-head h3 { margin:0; font-size:22px; line-height:1.25; }
    .modal-close { width:auto; min-height:0; margin:0; border:1px solid #c7d2fe; background:#f8fafc; color:#111827; padding:9px 12px; }
    .modal-close:hover { background:#eef2ff; }
    .modal-meta { margin:0 0 16px; }
    .modal-meta strong { margin-right:5px; }
    .modal-body p { margin:0 0 14px; line-height:1.45; }
    .modal-evidence { margin-top:18px; border-top:1px solid #e5e7eb; padding-top:14px; }
    .modal-evidence h4 { margin:0 0 8px; font-size:14px; }
    .modal-evidence div { color:#475569; font-size:13px; margin-top:5px; }
    @media (max-width: 900px) { .shell { grid-template-columns:1fr; } aside { border-right:0; border-bottom:1px solid var(--line); } main { padding:18px; } .metrics { grid-template-columns:1fr 1fr; } .finding { grid-template-columns:74px 1fr; } .title { grid-column:1 / -1; white-space:normal; } }
  </style>
</head>
<body>
  <div class="shell">
    <aside>
      <div class="brand">
        <h1>PlanCritic</h1>
        <div class="sub">Implementation plan review</div>
      </div>
      <form data-review-form hx-post="/check" hx-target="#results" hx-swap="innerHTML" hx-encoding="multipart/form-data">
        <input id="form_nonce" type="hidden" name="form_nonce" value="{{.FormNonce}}">
        <div class="group model-picker">
          <strong>Model</strong>
          <label for="provider">Provider</label>
          <select id="provider" name="provider">
            <option value="openai" {{if eq .DefaultProvider "openai"}}selected{{end}}>OpenAI</option>
            <option value="anthropic" {{if eq .DefaultProvider "anthropic"}}selected{{end}}>Anthropic</option>
            <option value="gemini" {{if eq .DefaultProvider "gemini"}}selected{{end}}>Gemini</option>
          </select>
          <label for="model">Model</label>
          <input id="model" name="model" value="{{.DefaultModel}}" placeholder="gpt-5.2" autocomplete="off" maxlength="120" aria-controls="model_options" aria-expanded="false">
          <div id="model_options" class="model-menu" role="listbox" hidden></div>
          <p id="model_picker_status" class="field-status" aria-live="polite"></p>
        </div>
        <label for="plan">Plan file</label>
        <input id="plan" name="plan" type="file" required>
        <label for="context">Context files</label>
        <input id="context" name="context" type="file" multiple>
        <label for="profile">Profile</label>
        <select id="profile" name="profile">{{range .Profiles}}<option value="{{.}}" {{if eq . $.DefaultProfile}}selected{{end}}>{{.}}</option>{{end}}</select>
        <label for="severity">Severity</label>
        <select id="severity" name="severity">
          <option value="info" {{if eq .DefaultSeverity "info"}}selected{{end}}>info</option>
          <option value="warn" {{if eq .DefaultSeverity "warn"}}selected{{end}}>warn</option>
          <option value="critical" {{if eq .DefaultSeverity "critical"}}selected{{end}}>critical</option>
        </select>
        <div class="twocol">
          <div><label for="max_issues">Max issues</label><input id="max_issues" name="max_issues" type="number" min="1" value="{{.DefaultMaxIssues}}"></div>
          <div><label for="max_questions">Max questions</label><input id="max_questions" name="max_questions" type="number" min="1" value="{{.DefaultMaxQuestions}}"></div>
        </div>
        <div class="row"><input id="strict" name="strict" type="checkbox" {{if .DefaultStrict}}checked{{end}}><label for="strict" style="margin:0">Strict</label></div>
        <div class="row"><input id="redact" name="redact" type="checkbox" {{if .DefaultRedact}}checked{{end}}><label for="redact" style="margin:0">Redact</label></div>
        <div class="row"><input id="no_cache" name="no_cache" type="checkbox" {{if .DefaultNoCache}}checked{{end}}><label for="no_cache" style="margin:0">No cache</label></div>
        <button type="submit" data-check-button>Check plan</button>
      </form>
    </aside>
    <main id="results"><div class="placeholder">Upload a plan file to run a review.</div></main>
  </div>
</body>
</html>`

const resultTemplate = `<div class="status">Completed in {{.Elapsed}}.</div>
<input id="form_nonce" type="hidden" name="form_nonce" value="{{.FormNonce}}" hx-swap-oob="true">
<section class="card">
  <div class="summary-head">
    <div>
      <div class="sub">Plan {{.PlanName}}</div>
      <div class="verdict">{{.Review.Summary.Verdict}}</div>
    </div>
    <div class="meta"><div class="pill"><b>Model</b>{{.ModelLabel}}</div><div class="pill"><b>Profile</b>{{.Review.Input.Profile}}</div></div>
  </div>
  <div class="metrics">
    <div class="metric"><b>Score</b><span>{{.Review.Summary.Score}}</span></div>
    <div class="metric critical"><b>Critical</b><span>{{.Review.Summary.CriticalCount}}</span></div>
    <div class="metric warn"><b>Warn</b><span>{{.Review.Summary.WarnCount}}</span></div>
    <div class="metric info"><b>Info</b><span>{{.Review.Summary.InfoCount}}</span></div>
  </div>
</section>
<section class="card">
  <h2>Findings</h2>
  {{if .Findings}}{{range .Findings}}<button type="button" class="finding {{.SeverityClass}}" data-modal-target="modal-{{.DOMID}}"><span class="badge {{.SeverityClass}}">{{.Severity}}</span><span class="id">{{.ID}}</span><span class="title">{{.Title}}</span></button>{{end}}{{else}}<div class="placeholder">No findings at the selected severity.</div>{{end}}
</section>
<section class="card">
  <h2>Plan Source</h2>
  <div class="source">{{range .PlanLines}}<div class="line {{if .Badges}}with-badges{{end}}"><div class="num">{{.Number}}</div><div class="code"><span class="line-text">{{.Text}}</span>{{if .Badges}}<span class="line-badges">{{range .Badges}}<button type="button" class="line-badge {{.SeverityClass}}" data-modal-target="modal-{{.DOMID}}">{{.Label}}</button>{{end}}</span>{{end}}</div></div>{{end}}</div>
</section>
{{range .Findings}}<div id="modal-{{.DOMID}}" class="modal" hidden>
  <div class="modal-box" role="dialog" aria-modal="true" aria-labelledby="title-{{.DOMID}}">
    <div class="modal-head"><h3 id="title-{{.DOMID}}">{{.ID}} {{.Title}}</h3><button type="button" class="modal-close" data-modal-close>Close</button></div>
    <div class="modal-meta"><strong>{{.Severity}}</strong>{{if .Category}} {{.Category}}{{end}}</div>
    <div class="modal-body">{{if .Detail}}{{range .Detail}}<p>{{.}}</p>{{end}}{{else}}<p>No additional detail was returned for this finding.</p>{{end}}</div>
    {{if .Evidence}}<div class="modal-evidence"><h4>Evidence</h4>{{range .Evidence}}<div>{{.Source}} {{.Path}}:{{.LineStart}}{{if ne .LineStart .LineEnd}}-{{.LineEnd}}{{end}}{{if .Quote}} - {{.Quote}}{{end}}</div>{{end}}</div>{{end}}
  </div>
</div>{{end}}`

const errorTemplate = `<div class="status error">{{.}}</div>`

const errorWithNonceTemplate = `<input id="form_nonce" type="hidden" name="form_nonce" value="{{.FormNonce}}" hx-swap-oob="true"><div class="status error">{{.Message}}</div>`

var (
	pageHTML           = template.Must(template.New("page").Parse(pageTemplate))
	resultHTML         = template.Must(template.New("result").Parse(resultTemplate))
	errorHTML          = template.Must(template.New("error").Parse(errorTemplate))
	errorWithNonceHTML = template.Must(template.New("errorWithNonce").Parse(errorWithNonceTemplate))
)
