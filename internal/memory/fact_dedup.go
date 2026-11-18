// fact_dedup.go — exact-text dedup for facts. A fact's words identify it:
// the fingerprint normalizes case, punctuation, and whitespace, so a
// re-extracted fact cannot land twice under two names.

package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

// bodyFingerprint hashes a normalized body: lowercased, every
// non-letter-or-digit run collapsed to one space, trimmed. "Make release!"
// and "make   release" share a fingerprint; different words never do.
func bodyFingerprint(body string) string {
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(body) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(b.String())))
	return hex.EncodeToString(sum[:])
}

// findDuplicateBody returns the name of the first fact whose body matches
// body's fingerprint, or "" when body is empty (bodyless facts stay
// distinct through their descriptions) or nothing matches.
func findDuplicateBody(facts []Fact, body string) string {
	if strings.TrimSpace(body) == "" {
		return ""
	}
	fingerprint := bodyFingerprint(body)
	for i := range facts {
		if bodyFingerprint(facts[i].Body) == fingerprint {
			return facts[i].Name
		}
	}
	return ""
}
