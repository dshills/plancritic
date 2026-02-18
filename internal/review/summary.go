package review

// ComputeSummary derives the verdict, score, and severity counts from issues.
func ComputeSummary(issues []Issue) Summary {
	var crit, warn, info int
	hasBlockingCritical := false

	for _, iss := range issues {
		switch iss.Severity {
		case SeverityCritical:
			crit++
			if iss.Blocking {
				hasBlockingCritical = true
			}
		case SeverityWarn:
			warn++
		case SeverityInfo:
			info++
		}
	}

	var verdict Verdict
	switch {
	case hasBlockingCritical:
		verdict = VerdictNotExecutable
	case crit > 0 || warn > 0:
		verdict = VerdictWithClarifications
	default:
		verdict = VerdictExecutable
	}

	return Summary{
		Verdict:       verdict,
		Score:         ComputeScore(issues),
		CriticalCount: crit,
		WarnCount:     warn,
		InfoCount:     info,
	}
}
