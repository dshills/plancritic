package review

// FilterBySeverity returns issues at or above the given threshold.
// Invalid severities are always included.
func FilterBySeverity(issues []Issue, threshold string) []Issue {
	minOrder := ThresholdOrder(threshold)
	var result []Issue
	for _, iss := range issues {
		if !iss.Severity.Valid() || iss.Severity.Order() <= minOrder {
			result = append(result, iss)
		}
	}
	return result
}

// FilterQuestionsBySeverity returns questions at or above the given threshold.
// Invalid severities are always included.
func FilterQuestionsBySeverity(questions []Question, threshold string) []Question {
	minOrder := ThresholdOrder(threshold)
	var result []Question
	for _, q := range questions {
		if !q.Severity.Valid() || q.Severity.Order() <= minOrder {
			result = append(result, q)
		}
	}
	return result
}
