package jobs

import "github.com/dkmnx/recollect/internal/memory"

func filterText(text string) string {
	return memory.Redact(memory.StripPrivateContent(text))
}

func privateOrSecret(text string) bool {
	return memory.StripPrivateContent(text) != text || memory.Redact(text) != text
}

// policySupported reports whether a job's privacy policy is current.
func policySupported(version int) bool {
	return version == privacyPolicyVersion1
}

// skipped reports whether a record must not reach a durable job.
func skipped(record HostRecord) bool {
	return record.Synthetic || memory.IsFullyPrivate(record.Text)
}

func filterRecords(records []HostRecord) []HostRecord {
	out := make([]HostRecord, 0, len(records))
	for _, record := range records {
		if skipped(record) {
			continue
		}
		record.Text = filterText(record.Text)
		out = append(out, record)
	}
	return out
}

// countIngested counts the records that the selected capture windows persist.
func countIngested(records []HostRecord, boundaries []RunBoundary) (accepted, dropped int) {
	seen := map[string]bool{}
	for _, boundary := range boundaries {
		if !captureBoundary(boundary.Kind) || !boundary.Available {
			continue
		}
		for _, record := range selectWindow(recordsFor(records, boundary)) {
			if seen[record.ID] {
				continue
			}
			seen[record.ID] = true
			if skipped(record) {
				dropped++
			} else {
				accepted++
			}
		}
	}
	return
}
