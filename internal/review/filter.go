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

// StripQuotes clears every evidence quote. Agents that already hold the
// plan in context pay for quotes twice; --no-quotes lets them keep the
// line references only.
func StripQuotes(r *Review) {
	for i := range r.Issues {
		for j := range r.Issues[i].Evidence {
			r.Issues[i].Evidence[j].Quote = ""
		}
	}
	for i := range r.Questions {
		for j := range r.Questions[i].Evidence {
			r.Questions[i].Evidence[j].Quote = ""
		}
	}
	if c := r.Coverage; c != nil {
		for i := range c.Requirements {
			for j := range c.Requirements[i].SpecEvidence {
				c.Requirements[i].SpecEvidence[j].Quote = ""
			}
			for j := range c.Requirements[i].PlanEvidence {
				c.Requirements[i].PlanEvidence[j].Quote = ""
			}
		}
		for i := range c.OutOfScope {
			for j := range c.OutOfScope[i].PlanEvidence {
				c.OutOfScope[i].PlanEvidence[j].Quote = ""
			}
		}
	}
}
