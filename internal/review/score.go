package review

// ComputeScore calculates a deterministic score from issue severity counts.
// Starts at 100, subtracts 20 per CRITICAL, 7 per WARN, 2 per INFO, clamps at 0.
func ComputeScore(issues []Issue) int {
	score := 100
	for _, iss := range issues {
		switch iss.Severity {
		case SeverityCritical:
			score -= 20
		case SeverityWarn:
			score -= 7
		case SeverityInfo:
			score -= 2
		}
	}
	if score < 0 {
		score = 0
	}
	return score
}
