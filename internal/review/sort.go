package review

import "sort"

// SortIssues sorts issues by severity (CRITICAL > WARN > INFO),
// then by first evidence line_start ascending.
func SortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		oi := issues[i].Severity.order()
		oj := issues[j].Severity.order()
		if oi != oj {
			return oi < oj
		}
		return firstLine(issues[i].Evidence) < firstLine(issues[j].Evidence)
	})
}

// SortQuestions sorts questions by severity then by first evidence line_start.
func SortQuestions(questions []Question) {
	sort.SliceStable(questions, func(i, j int) bool {
		oi := questions[i].Severity.order()
		oj := questions[j].Severity.order()
		if oi != oj {
			return oi < oj
		}
		return firstLine(questions[i].Evidence) < firstLine(questions[j].Evidence)
	})
}

func firstLine(ev []Evidence) int {
	if len(ev) == 0 {
		return 0
	}
	return ev[0].LineStart
}
