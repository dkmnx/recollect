// index.go — MEMORY.md rendering and prompt-injection truncation. The index
// is derived state: regenerated from the fact files on every mutation,
// newest first, never refused for size. Injection caps (200 lines /
// 25,000 characters) are the only size guard in the system.

package memory

import (
	"fmt"
	"sort"
	"strings"
)

// The injection caps. They are format constants (docs/memory.md),
// not settings: xi truncates what the model sees the way ZCode does.
const (
	IndexLineLimit = 200
	IndexCharLimit = 25_000
)

// RenderIndex renders MEMORY.md from the facts: one `- [name](path) —
// description` line per fact, newest first, ties by name.
func RenderIndex(facts []Fact) string {
	sorted := make([]Fact, len(facts))
	copy(sorted, facts)
	sort.SliceStable(sorted, func(i, j int) bool {
		if !sorted[i].ModTime.Equal(sorted[j].ModTime) {
			return sorted[i].ModTime.After(sorted[j].ModTime)
		}
		return sorted[i].Name < sorted[j].Name
	})
	var b strings.Builder
	for _, fact := range sorted {
		b.WriteString("- [" + fact.Name + "](" + fact.RelPath() + ") — " + CollapseLine(fact.Description) + "\n")
	}
	return b.String()
}

// TruncateIndex applies the injection caps to index content. Content within
// both caps is returned unchanged; over-cap content is cut and carries the
// warning, which mirrors ZCode's index truncation so both agents steer the
// model the same way.
func TruncateIndex(content string) string {
	content = strings.Trim(content, "\n")
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	lineTruncated := len(lines) > IndexLineLimit
	charTruncated := len(content) > IndexCharLimit
	if !lineTruncated && !charTruncated {
		return content
	}
	truncated := content
	if lineTruncated {
		truncated = strings.Join(lines[:IndexLineLimit], "\n")
	}
	if len(truncated) > IndexCharLimit {
		if cut, _ := HeadBytes(truncated, IndexCharLimit); cut != "" {
			if at := strings.LastIndex(cut, "\n"); at > 0 {
				cut = cut[:at]
			}
			truncated = cut
		}
	}
	return truncated + "\n\n" + indexWarning(len(lines), len(content)) + "\n"
}

// indexWarning names what was over the cap and the trim prescription.
func indexWarning(lineCount, charCount int) string {
	switch {
	case lineTruncatedOnly(lineCount, charCount):
		return fmt.Sprintf("> WARNING: MEMORY.md is %d lines (limit: %d). Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files.", lineCount, IndexLineLimit)
	case charTruncatedOnly(lineCount, charCount):
		return fmt.Sprintf("> WARNING: MEMORY.md is %s (limit: %d bytes) — index entries are too long. Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files.", formatBytes(charCount), IndexCharLimit)
	default:
		return fmt.Sprintf("> WARNING: MEMORY.md is %d lines and %s. Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files.", lineCount, formatBytes(charCount))
	}
}

func lineTruncatedOnly(lineCount, charCount int) bool {
	return lineCount > IndexLineLimit && charCount <= IndexCharLimit
}

func charTruncatedOnly(lineCount, charCount int) bool {
	return charCount > IndexCharLimit && lineCount <= IndexLineLimit
}

// formatBytes renders a byte count for the warning line.
func formatBytes(value int) string {
	switch {
	case value < 1024:
		return fmt.Sprintf("%d bytes", value)
	case value < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(value)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(value)/(1024*1024))
	}
}

// sessionLineMaxBytes clamps the sessions continuity line.
const sessionLineMaxBytes = 160

// IndexInput is the material the prompt block renders.
type IndexInput struct {
	Name      string
	Root      string
	IndexPath string
	Index     string // raw MEMORY.md content
	Digests   []Digest
}

// BuildIndex renders the project memory block for the system prompt: the
// capped index inside a project_memory envelope, with the newest session
// digest as the continuity anchor. Deterministic for the same inputs.
func BuildIndex(in IndexInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<project_memory name=%q root=%q path=%q>\n", in.Name, in.Root, in.IndexPath)
	if body := TruncateIndex(in.Index); body != "" {
		b.WriteString(body + "\n")
	}
	if line := renderSessions(in.Digests); line != "" {
		b.WriteString(line)
	}
	b.WriteString("</project_memory>\n")
	return b.String()
}

// renderSessions renders the newest digest as the continuity anchor.
func renderSessions(digests []Digest) string {
	if len(digests) == 0 {
		return ""
	}
	latest := digests[0]
	line := "sessions: " + latest.SessionID
	if topic := CollapseLine(latest.Topic); topic != "" {
		line += " — " + topic
	}
	if summary := DigestLine(latest.Summary); summary != "" {
		line += ": " + summary
	}
	if len(digests) > 1 {
		line += fmt.Sprintf("; (%d more)", len(digests)-1)
	}
	return line + "\n"
}

// clampLine clamps a rendered summary, marking the cut with an ellipsis.
func clampLine(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head, _ := HeadBytes(s, max)
	return strings.TrimSpace(head) + "…"
}

// DigestLine renders a digest topic or summary as one clamped line. The
// injected continuity anchor and a session-digest list row both read it
// through here: one line keeps the prompt envelope well formed, and the byte
// bound keeps a row from carrying a model-authored field whole.
func DigestLine(field string) string {
	return clampLine(CollapseLine(field), sessionLineMaxBytes)
}
