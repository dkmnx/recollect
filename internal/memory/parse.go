package memory

import (
	"strings"
	"unicode/utf8"
)

// NormalizeNewlines folds CRLF and lone CR to LF so parsers see one shape.
func NormalizeNewlines(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// CollapseLine flattens whitespace runs into single spaces and trims.
func CollapseLine(s string) string {
	return strings.Join(strings.Fields(NormalizeNewlines(s)), " ")
}

// TailBytes returns the last max bytes of s, cut on a rune boundary, and
// whether anything was dropped. A non-positive max keeps nothing.
func TailBytes(s string, max int) (string, bool) {
	if max <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= max {
		return s, false
	}
	start := len(s) - max
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:], true
}

// HeadBytes returns the first max bytes of s, cut on a rune boundary, and
// whether anything was dropped. A non-positive max keeps nothing.
func HeadBytes(s string, max int) (string, bool) {
	if max <= 0 {
		return "", len(s) > 0
	}
	if len(s) <= max {
		return s, false
	}
	end := max
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}
