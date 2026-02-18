package review

// Verdict indicates the overall executability of the plan.
type Verdict string

const (
	VerdictExecutable              Verdict = "EXECUTABLE_AS_IS"
	VerdictWithClarifications      Verdict = "EXECUTABLE_WITH_CLARIFICATIONS"
	VerdictNotExecutable           Verdict = "NOT_EXECUTABLE"
)

func (v Verdict) Valid() bool {
	switch v {
	case VerdictExecutable, VerdictWithClarifications, VerdictNotExecutable:
		return true
	}
	return false
}

// Severity indicates the importance of an issue or question.
type Severity string

const (
	SeverityInfo     Severity = "INFO"
	SeverityWarn     Severity = "WARN"
	SeverityCritical Severity = "CRITICAL"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityWarn, SeverityCritical:
		return true
	}
	return false
}

// severityOrder returns a sort key (lower = higher priority).
func (s Severity) order() int {
	switch s {
	case SeverityCritical:
		return 0
	case SeverityWarn:
		return 1
	case SeverityInfo:
		return 2
	default:
		return 3
	}
}

// Category classifies the type of issue found.
type Category string

const (
	CategoryContradiction            Category = "CONTRADICTION"
	CategoryAmbiguity                Category = "AMBIGUITY"
	CategoryMissingPrerequisite      Category = "MISSING_PREREQUISITE"
	CategoryMissingAcceptanceCriteria Category = "MISSING_ACCEPTANCE_CRITERIA"
	CategoryRiskSecurity             Category = "RISK_SECURITY"
	CategoryRiskData                 Category = "RISK_DATA"
	CategoryRiskOperations           Category = "RISK_OPERATIONS"
	CategoryTestGap                  Category = "TEST_GAP"
	CategoryScopeCreepRisk           Category = "SCOPE_CREEP_RISK"
	CategoryUnrealisticStep          Category = "UNREALISTIC_STEP"
	CategoryOrderingDependency       Category = "ORDERING_DEPENDENCY"
	CategoryUnspecifiedInterface     Category = "UNSPECIFIED_INTERFACE"
	CategoryNonDeterminism           Category = "NON_DETERMINISM"
)

func (c Category) Valid() bool {
	switch c {
	case CategoryContradiction, CategoryAmbiguity, CategoryMissingPrerequisite,
		CategoryMissingAcceptanceCriteria, CategoryRiskSecurity, CategoryRiskData,
		CategoryRiskOperations, CategoryTestGap, CategoryScopeCreepRisk,
		CategoryUnrealisticStep, CategoryOrderingDependency,
		CategoryUnspecifiedInterface, CategoryNonDeterminism:
		return true
	}
	return false
}

// PatchType classifies the type of patch.
type PatchType string

const (
	PatchTypePlanTextEdit PatchType = "PLAN_TEXT_EDIT"
)

func (p PatchType) Valid() bool {
	return p == PatchTypePlanTextEdit
}

// CheckStatus indicates the result of a checklist item.
type CheckStatus string

const (
	CheckStatusPass CheckStatus = "PASS"
	CheckStatusFail CheckStatus = "FAIL"
	CheckStatusNA   CheckStatus = "N/A"
)

func (cs CheckStatus) Valid() bool {
	switch cs {
	case CheckStatusPass, CheckStatusFail, CheckStatusNA:
		return true
	}
	return false
}
