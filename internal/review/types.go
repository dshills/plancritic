// Package review defines the core types for PlanCritic review output.
package review

// Review is the top-level output object.
type Review struct {
	Tool       string      `json:"tool"`
	Version    string      `json:"version"`
	Input      Input       `json:"input"`
	Summary    Summary     `json:"summary"`
	Questions  []Question  `json:"questions"`
	Issues     []Issue     `json:"issues"`
	Patches    []Patch     `json:"patches,omitempty"`
	Checklists []Checklist `json:"checklists,omitempty"`
	// Delta is present only when a --baseline was supplied.
	Delta *Delta `json:"delta,omitempty"`
	// Coverage is present only when a --spec was supplied.
	Coverage *Coverage `json:"coverage,omitempty"`
	Meta     Meta      `json:"meta"`
}

// Input describes the files and settings used for the review.
type Input struct {
	PlanFile     string        `json:"plan_file"`
	PlanHash     string        `json:"plan_hash"`
	ContextFiles []ContextFile `json:"context_files,omitempty"`
	Profile      string        `json:"profile,omitempty"`
	Strict       bool          `json:"strict"`
}

// ContextFile records a context file path and its hash.
type ContextFile struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
}

// Summary holds the verdict, score, and severity counts.
type Summary struct {
	Verdict       Verdict `json:"verdict"`
	Score         int     `json:"score"`
	CriticalCount int     `json:"critical_count"`
	WarnCount     int     `json:"warn_count"`
	InfoCount     int     `json:"info_count"`
}

// Issue represents a detected problem in the plan.
type Issue struct {
	ID             string     `json:"id"`
	Severity       Severity   `json:"severity"`
	Category       Category   `json:"category"`
	Title          string     `json:"title"`
	Description    string     `json:"description"`
	Evidence       []Evidence `json:"evidence"`
	Impact         string     `json:"impact"`
	Recommendation string     `json:"recommendation"`
	Blocking       bool       `json:"blocking"`
	Tags           []string   `json:"tags,omitempty"`
	// Fingerprint identifies the finding across runs (see fingerprint.go).
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Question represents an ambiguity that must be resolved.
type Question struct {
	ID               string     `json:"id"`
	Severity         Severity   `json:"severity"`
	Question         string     `json:"question"`
	WhyNeeded        string     `json:"why_needed"`
	Blocks           []string   `json:"blocks,omitempty"`
	Evidence         []Evidence `json:"evidence"`
	SuggestedAnswers []string   `json:"suggested_answers,omitempty"`
	// Fingerprint identifies the question across runs (see fingerprint.go).
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Patch is an optional suggested edit to the plan text.
type Patch struct {
	ID          string    `json:"id"`
	Type        PatchType `json:"type"`
	Title       string    `json:"title"`
	DiffUnified string    `json:"diff_unified"`
}

// Checklist records the result of a profile checklist evaluation.
type Checklist struct {
	ID     string      `json:"id"`
	Title  string      `json:"title"`
	Checks []CheckItem `json:"checks"`
}

// CheckItem is a single check within a checklist.
type CheckItem struct {
	Check  string      `json:"check"`
	Status CheckStatus `json:"status"`
}

// Evidence references a specific location in the plan or context.
type Evidence struct {
	Source    string `json:"source"`
	Path      string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Quote     string `json:"quote,omitempty"`
}

// Meta records the model and settings used for the review.
type Meta struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	// Cached is true when the review was served from the local result
	// cache rather than a fresh provider call.
	Cached bool `json:"cached,omitempty"`
	// Truncated is true when the model hit its output cap and the review
	// was salvaged from the complete prefix; findings may be missing.
	Truncated bool `json:"truncated,omitempty"`
	// Usage totals the provider tokens spent on this review across the
	// main call and any repair call. Absent on a result-cache hit.
	Usage *Usage `json:"usage,omitempty"`
}

// Usage is the token accounting for one review.
type Usage struct {
	Calls                    int `json:"calls"`
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
}

// Coverage maps the specification's requirements onto the plan. It is
// produced only when a spec is supplied (--spec).
type Coverage struct {
	Requirements []Requirement   `json:"requirements"`
	OutOfScope   []ScopeItem     `json:"out_of_scope"`
	Summary      CoverageSummary `json:"summary"`
}

// CoverageStatus says how well the plan addresses a requirement.
type CoverageStatus string

const (
	CoverageCovered   CoverageStatus = "COVERED"
	CoveragePartial   CoverageStatus = "PARTIAL"
	CoverageUncovered CoverageStatus = "UNCOVERED"
)

// Valid reports whether s is a known coverage status.
func (s CoverageStatus) Valid() bool {
	switch s {
	case CoverageCovered, CoveragePartial, CoverageUncovered:
		return true
	}
	return false
}

// Requirement is one requirement found in the spec and where (if
// anywhere) the plan implements it.
type Requirement struct {
	ID           string         `json:"id"`
	Requirement  string         `json:"requirement"`
	Status       CoverageStatus `json:"status"`
	SpecEvidence []Evidence     `json:"spec_evidence"`
	PlanEvidence []Evidence     `json:"plan_evidence"`
	Note         string         `json:"note,omitempty"`
}

// ScopeItem is plan work with no basis in the spec.
type ScopeItem struct {
	ID           string     `json:"id"`
	PlanStep     string     `json:"plan_step"`
	PlanEvidence []Evidence `json:"plan_evidence"`
	Note         string     `json:"note,omitempty"`
}

// CoverageSummary counts requirements by status. It is computed by the
// tool from the entries, never taken from the model.
type CoverageSummary struct {
	Covered    int `json:"covered"`
	Partial    int `json:"partial"`
	Uncovered  int `json:"uncovered"`
	OutOfScope int `json:"out_of_scope"`
}

// ComputeCoverageSummary fills c.Summary from c's entries.
func ComputeCoverageSummary(c *Coverage) {
	c.Summary = CoverageSummary{OutOfScope: len(c.OutOfScope)}
	for _, r := range c.Requirements {
		switch r.Status {
		case CoverageCovered:
			c.Summary.Covered++
		case CoveragePartial:
			c.Summary.Partial++
		case CoverageUncovered:
			c.Summary.Uncovered++
		}
	}
}
