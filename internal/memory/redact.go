// redact.go — best-effort redaction for capture inputs and outputs: every
// secret-like match becomes [redacted]. Regex filters are not a guarantee;
// the capture path also skips entries that still look secret-like.

package memory

// RedactedPlaceholder replaces secret-like matches.
const RedactedPlaceholder = "[redacted]"

// Redact replaces every secret-like match with the placeholder.
func Redact(text string) string {
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, RedactedPlaceholder)
	}
	return text
}
