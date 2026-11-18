package memory

import "strings"

// Match is one search hit: a 1-based line number plus one line of context on
// either side.
type Match struct {
	Line    int      `json:"line"`
	Text    string   `json:"text"`
	Context []string `json:"context,omitempty"`
}

// SearchLines finds case-insensitive substring matches, capped at max
// results (non-positive max means no cap).
func SearchLines(content, query string, max int) []Match {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	lower := strings.ToLower(query)
	lines := strings.Split(content, "\n")
	var matches []Match
	for i, line := range lines {
		if !strings.Contains(strings.ToLower(line), lower) {
			continue
		}
		from := i - 1
		if from < 0 {
			from = 0
		}
		to := i + 2
		if to > len(lines) {
			to = len(lines)
		}
		matches = append(matches, Match{
			Line:    i + 1,
			Text:    line,
			Context: append([]string(nil), lines[from:to]...),
		})
		if max > 0 && len(matches) >= max {
			break
		}
	}
	return matches
}
