// secret.go — best-effort secret detection for memory writes: explicit saves
// fail loudly on secret-like content, automatic capture skips it. The
// patterns are deliberately narrow; this is a filter, not a guarantee.

package memory

import "regexp"

// SecretPatterns exposes the secret matchers for the obfuscation pass,
// which needs spans (not just a verdict) to mint placeholders.
func SecretPatterns() []*regexp.Regexp { return secretPatterns }

var secretPatterns = []*regexp.Regexp{
	// Direct provider/infra key names, including compound forms such as
	// API_WRITE_KEY and phrases like "API key".
	regexp.MustCompile(`(?i)\b[a-z0-9_ -]*(?:api[a-z0-9_ -]*key|secret[a-z0-9_ -]*|token[a-z0-9_ -]*|password[a-z0-9_ -]*|passwd[a-z0-9_ -]*|credential[a-z0-9_ -]*|auth[a-z0-9_ -]*)[a-z0-9_ -]*\s*[:=]\s*["']?[A-Za-z0-9_\-]{12,}`),
	// AWS access key IDs.
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	// OpenAI/Anthropic style keys.
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`),
	// GitHub tokens.
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	// Slack tokens.
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	// Private key blocks.
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// Bearer auth headers.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]{24,}`),
	// JWT-like tokens.
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}`),
	// Credentials embedded in URL userinfo (scheme://user:password@host).
	regexp.MustCompile(`(?i)\w+://[^/\s:@]+:[^/@\s]+@`),
}

// SecretLike reports whether text carries a secret-like pattern.
func SecretLike(text string) bool {
	for _, pattern := range secretPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}
