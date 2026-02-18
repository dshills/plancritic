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
	Meta       Meta        `json:"meta"`
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
	ID             string   `json:"id"`
	Severity       Severity `json:"severity"`
	Category       Category `json:"category"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Evidence       []Evidence `json:"evidence"`
	Impact         string   `json:"impact"`
	Recommendation string   `json:"recommendation"`
	Blocking       bool     `json:"blocking"`
	Tags           []string `json:"tags,omitempty"`
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
	ID     string       `json:"id"`
	Title  string       `json:"title"`
	Checks []CheckItem  `json:"checks"`
}

// CheckItem is a single check within a checklist.
type CheckItem struct {
	Check  string      `json:"check"`
	Status CheckStatus `json:"status"`
}

// Evidence references a specific location in the plan or context.
type Evidence struct {
	Source    string `json:"source"`
	Path     string `json:"path"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Quote    string `json:"quote"`
}

// Meta records the model and settings used for the review.
type Meta struct {
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
}
