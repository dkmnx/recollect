package rpc

import "github.com/dkmnx/recollect/internal/memory"

func filterModelText(text string) string {
	return memory.Redact(memory.StripPrivateContent(text))
}

// filterMatches filters user search hits without changing stored lines.
func filterMatches(matches []memory.Match) []memory.Match {
	out := make([]memory.Match, 0, len(matches))
	for _, match := range matches {
		match.Text = filterModelText(match.Text)
		if len(match.Context) > 0 {
			context := make([]string, len(match.Context))
			for i, line := range match.Context {
				context[i] = filterModelText(line)
			}
			match.Context = context
		}
		out = append(out, match)
	}
	return out
}
