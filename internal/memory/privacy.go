// privacy.go — redaction for content the user marks as private. A
// <private>...</private> span is replaced with [REDACTED] before it lands
// in memory; an unclosed span redacts to the end of the input. This is
// best-effort redaction, not a guarantee, and it sits beside the secret
// filter rather than replacing it.

package memory

import (
	"regexp"
	"strings"
)

var privateTagPattern = regexp.MustCompile(`(?i)<(/?)private\s*>`)

// StripPrivateContent replaces every <private>...</private> span with
// [REDACTED]. An unclosed opening tag redacts to the end of the input;
// a stray closing tag is dropped.
func StripPrivateContent(content string) string {
	if !strings.Contains(content, "<") {
		return content
	}
	matches := privateTagPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return content
	}
	var result strings.Builder
	depth := 0
	cursor := 0
	for _, loc := range matches {
		isClosing := content[loc[2]:loc[3]] == "/"
		if !isClosing {
			if depth == 0 {
				result.WriteString(content[cursor:loc[0]])
			}
			depth++
			cursor = loc[1]
			continue
		}
		if depth == 0 {
			result.WriteString(content[cursor:loc[0]])
			cursor = loc[1]
			continue
		}
		depth--
		if depth == 0 {
			result.WriteString("[REDACTED]")
		}
		cursor = loc[1]
	}
	if depth > 0 {
		return result.String() + "[REDACTED]"
	}
	return result.String() + content[cursor:]
}

// IsFullyPrivate reports whether nothing survives redaction.
func IsFullyPrivate(content string) bool {
	stripped := strings.TrimSpace(StripPrivateContent(content))
	return stripped == "[REDACTED]" || stripped == ""
}
