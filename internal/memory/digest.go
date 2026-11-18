// digest.go — per-session handoff digests: one file per session id with its
// metadata and a short summary. The index lists the latest for continuity;
// recall reads them by session id. Storage and pruning live in the store.

package memory

import (
	"strings"
	"time"
)

// Digest is one session handoff record. Date is the session start, Updated
// is the last capture; retention ages from Updated.
type Digest struct {
	SessionID string
	Topic     string
	Summary   string
	Date      time.Time
	Updated   time.Time
	Fallback  bool
	Path      string
}

// digestTimeLayout is the metadata timestamp format.
const digestTimeLayout = time.RFC3339

// DefaultDigestMaxBytes bounds how much summary text the deterministic
// fallback digest renders. It is a generation budget, not a write refusal:
// model-written digests are never size-checked.
const DefaultDigestMaxBytes = 4 * 1024

// FormatDigest renders the canonical digest file.
func FormatDigest(digest Digest) string {
	var b strings.Builder
	if topic := CollapseLine(digest.Topic); topic != "" {
		b.WriteString("# " + topic + "\n")
	}
	b.WriteString("Session: " + digest.SessionID + "\n")
	if !digest.Date.IsZero() {
		b.WriteString("Date: " + digest.Date.UTC().Format(digestTimeLayout) + "\n")
	}
	if !digest.Updated.IsZero() {
		b.WriteString("Updated: " + digest.Updated.UTC().Format(digestTimeLayout) + "\n")
	}
	if digest.Fallback {
		b.WriteString("Fallback: true\n")
	}
	if summary := strings.Trim(digest.Summary, "\n"); summary != "" {
		b.WriteString("\n" + summary + "\n")
	}
	return b.String()
}

// ParseDigest reads a digest file. ok is false without a Session line, so a
// stray Markdown file in the sessions directory is ignored.
func ParseDigest(content, path string) (Digest, bool) {
	normalized := NormalizeNewlines(strings.TrimPrefix(content, "\ufeff"))
	digest := Digest{Path: path}
	var body strings.Builder
	metaOpen := true
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if metaOpen {
			if trimmed == "" {
				metaOpen = false
				continue
			}
			if heading, ok := strings.CutPrefix(trimmed, "# "); ok {
				digest.Topic = CollapseLine(heading)
				continue
			}
			lower := strings.ToLower(trimmed)
			switch {
			case strings.HasPrefix(lower, "session:"):
				digest.SessionID = strings.TrimSpace(trimmed[len("session:"):])
				continue
			case strings.HasPrefix(lower, "date:"):
				digest.Date, _ = time.Parse(digestTimeLayout, strings.TrimSpace(trimmed[len("date:"):]))
				continue
			case strings.HasPrefix(lower, "updated:"):
				digest.Updated, _ = time.Parse(digestTimeLayout, strings.TrimSpace(trimmed[len("updated:"):]))
				continue
			case strings.HasPrefix(lower, "fallback:"):
				digest.Fallback = parseBoolValue(trimmed[len("fallback:"):])
				continue
			}
			metaOpen = false
		}
		body.WriteString(line)
		body.WriteByte('\n')
	}
	digest.Summary = strings.Trim(body.String(), "\n")
	if strings.TrimSpace(digest.SessionID) == "" {
		return Digest{}, false
	}
	return digest, true
}

// parseBoolValue reads the loose boolean spellings used in metadata.
func parseBoolValue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

// DigestRetention bounds the searchable digest set: the newest MaxCount and
// no older than TTLDays by last capture. Zero disables a bound.
type DigestRetention struct {
	MaxCount int
	TTLDays  int
}

// RetainDigests splits digests into the searchable set and the archive set.
// The newest digest is always kept, regardless of age.
func RetainDigests(digests []Digest, now time.Time, retention DigestRetention) (keep, archive []Digest) {
	sorted := append([]Digest(nil), digests...)
	sortDigests(sorted)
	var cutoff time.Time
	if retention.TTLDays > 0 {
		cutoff = now.AddDate(0, 0, -retention.TTLDays)
	}
	for i, digest := range sorted {
		kept := retention.MaxCount <= 0 || i < retention.MaxCount
		if kept && !cutoff.IsZero() && i > 0 && digestStamp(digest).Before(cutoff) {
			kept = false
		}
		if kept {
			keep = append(keep, digest)
		} else {
			archive = append(archive, digest)
		}
	}
	return keep, archive
}

// MinDigestSummaryBytes is the shortest summary worth storing.
const MinDigestSummaryBytes = 20

// DigestUsable reports whether a digest carries enough content to store.
func DigestUsable(digest Digest) bool {
	return strings.TrimSpace(digest.SessionID) != "" &&
		len(strings.TrimSpace(digest.Summary)) >= MinDigestSummaryBytes
}

// digestStamp is the retention timestamp: last capture, then session start.
func digestStamp(digest Digest) time.Time {
	if !digest.Updated.IsZero() {
		return digest.Updated
	}
	return digest.Date
}
