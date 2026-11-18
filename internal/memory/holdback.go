// holdback.go — the streaming counterpart to redaction: how much of a
// buffer's tail could still grow into a secret, and so must not be handed
// to a reader yet.

package memory

import "strings"

// Character sets the anchored patterns accept in a token body.
const (
	secretAlnum = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	secretWord  = secretAlnum + "_-"
	secretHost  = secretAlnum + "._-"
)

// secretLead is a literal lead-in from secretPatterns together with the
// body its matcher needs after it. spaced marks a matcher that allows
// whitespace between the lead and the body (the Bearer header).
type secretLead struct {
	lead    string // lower case; matched case-insensitively
	body    string // characters the matcher accepts after the lead
	minTail int    // how many body characters the matcher needs
	spaced  bool
}

// secretLeads mirrors the anchored patterns in secretPatterns. A lead
// followed by fewer than minTail body characters — all of them in body —
// is a match that has not completed yet.
var secretLeads = []secretLead{
	{"akia", "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ", 16, false}, // AKIA[0-9A-Z]{16}
	{"sk-", secretWord, 20, false},                              // sk-[A-Za-z0-9_-]{20,}
	{"ghp_", secretAlnum, 20, false},                            // gh[pousr]_[A-Za-z0-9]{20,}
	{"gho_", secretAlnum, 20, false},
	{"ghu_", secretAlnum, 20, false},
	{"ghs_", secretAlnum, 20, false},
	{"ghr_", secretAlnum, 20, false},
	{"xoxa-", secretAlnum + "-", 10, false}, // xox[baprs]-[A-Za-z0-9-]{10,}
	{"xoxb-", secretAlnum + "-", 10, false},
	{"xoxp-", secretAlnum + "-", 10, false},
	{"xoxr-", secretAlnum + "-", 10, false},
	{"xoxs-", secretAlnum + "-", 10, false},
	{"bearer", secretHost, 24, true}, // (?i)\bbearer\s+[A-Za-z0-9._-]{24,}
}

// secretNames are the assignment names the key=value pattern requires
// before its separator.
var secretNames = []string{"key", "secret", "token", "password", "passwd", "credential", "auth"}

// SecretHoldback reports how many trailing bytes of s a streamed reader
// must withhold: the tail that could still grow into a secret match.
//
// Redact runs over a whole buffer, so it fires only on a complete match. A
// buffer that ends mid-secret carries a prefix no matcher sees, and a
// reader handed that prefix learns the start of the credential — up to the
// matcher's whole minimum window. Withholding the tail until the match
// completes (redaction then fires and the tail is re-delivered redacted) or
// the producer settles (it can no longer complete, so it is safe as
// written) closes that window.
//
// The checks below mirror secretPatterns. The PRIVATE KEY banner needs no
// entry: its whole match is its own lead-in, so no prefix of it can be a
// secret the banner would have redacted.
func SecretHoldback(s string) int {
	if s == "" || !canEndSecret(s[len(s)-1]) {
		// A partial match must end on a byte a secret can contain. Output
		// almost always ends on a newline or a space, so this gate keeps
		// the scan off the common path.
		return 0
	}
	best := 0
	consider := func(n int) {
		if n > best {
			best = n
		}
	}
	lower := strings.ToLower(s)

	// Anchored tokens: withhold from the lead while its body is still too
	// short to match. Every occurrence is considered, not just the last: a
	// token can contain its own lead-in (a JWT's later segments start with
	// eyJ, the base64 of `{"`), and measuring from the inner one would
	// deliver the outer token's head.
	for _, l := range secretLeads {
		for at := 0; at < len(lower); {
			j := strings.Index(lower[at:], l.lead)
			if j < 0 {
				break
			}
			i := at + j
			at = i + len(l.lead)
			body := s[i+len(l.lead):]
			if l.spaced {
				body = strings.TrimLeft(body, " \t")
			}
			if len(body) >= l.minTail || !allIn(body, l.body) {
				continue
			}
			consider(len(s) - i)
		}
	}

	// JWT: eyJ<20+>.<20+>. Either segment still short, or no dot yet, means
	// the match has not completed.
	for at := 0; at < len(s); {
		j := strings.Index(s[at:], "eyJ")
		if j < 0 {
			break
		}
		i := at + j
		at = i + 3
		body := s[i+3:]
		if !allIn(body, secretWord+".") {
			continue
		}
		if dot := strings.IndexByte(body, '.'); dot < 20 || len(body)-dot-1 < 20 {
			consider(len(s) - i)
		}
	}

	// Assignment: name = value, with the value still short. The name check
	// mirrors the pattern's own requirement, so ordinary text such as
	// "level:debug" is not held back.
	if i := strings.LastIndexAny(s, "=:"); i >= 0 && namesSecret(s[:i]) {
		if body := strings.TrimLeft(s[i+1:], ` "'`); len(body) < 12 && allIn(body, secretWord) {
			consider(len(s) - i - 1)
		}
	}

	// URL userinfo: the password has not reached its @ yet.
	if i := strings.LastIndex(s, "://"); i >= 0 {
		if c := strings.IndexByte(s[i+3:], ':'); c >= 0 {
			if pw := s[i+3+c+1:]; pw != "" && !strings.ContainsAny(pw, "@/ \t\r\n") {
				consider(len(pw))
			}
		}
	}

	return best
}

// canEndSecret reports whether b can be the last byte of a partial secret.
// The userinfo password class is [^/@\s], so every byte but whitespace, "/"
// and "@" can; those are also what streamed output usually ends on, which
// keeps the scan off the common path.
func canEndSecret(b byte) bool {
	switch b {
	case '/', '@', ' ', '\t', '\r', '\n', '\v', '\f':
		return false
	}
	return true
}

// namesSecret reports whether the text before an assignment separator ends
// in a name the key=value pattern accepts.
func namesSecret(before string) bool {
	i := len(before)
	for i > 0 && isNameByte(before[i-1]) {
		i--
	}
	name := strings.ToLower(before[i:])
	for _, n := range secretNames {
		if strings.Contains(name, n) {
			return true
		}
	}
	return false
}

// isNameByte matches the pattern's name class, [a-z0-9_ -], plus upper
// case.
func isNameByte(b byte) bool {
	return b == '_' || b == '-' || b == ' ' ||
		(b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// allIn reports whether every byte of s is in set (both ASCII).
func allIn(s, set string) bool {
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(set, rune(s[i])) {
			return false
		}
	}
	return true
}
