// journal.go — the project change journal: one JSONL line per store
// mutation, written inside the mutation funnels so no writer can forget
// it. The journal is provenance for humans and a future undo, not state
// the memory paths read: a missing, blocked, or rotated journal costs no
// fact and no marker, so every failure here is best-effort like the daily
// snapshot.

package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// journalRotateBytes bounds one journal generation: past it, the file
// becomes journal.prev.jsonl and a fresh journal starts. The pair stays
// bounded at twice the bound.
const journalRotateBytes = 1 << 20

// journalSummaryMaxBytes clamps the old and new descriptions per entry.
const journalSummaryMaxBytes = 200

// JournalEntry is one recorded store mutation. Old and New carry the
// description before and after when it changed; Session is the fact's
// OriginSessionID, which both capture and tool writes stamp.
type JournalEntry struct {
	TS      time.Time `json:"ts"`
	Op      string    `json:"op"`
	Key     string    `json:"key"`
	Old     string    `json:"old,omitempty"`
	New     string    `json:"new,omitempty"`
	Session string    `json:"session,omitempty"`
}

// journalLocked appends one entry, rotating at the bound. Callers hold the
// store mutex and have already committed their mutation: a journal failure
// must not fail the operation it rode on.
func (s *Store) journalLocked(entry JournalEntry) {
	if entry.TS.IsZero() {
		entry.TS = s.now()
	}
	path := JournalFile(s.projectDir)
	if info, err := os.Lstat(path); err == nil && info.Size() >= journalRotateBytes {
		prev := filepath.Join(s.projectDir, journalPrevFile)
		_ = os.Remove(prev)
		_ = os.Rename(path, prev)
	}
	current, err := readMemoryFile(path)
	if err != nil {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_ = s.writeLocked(path, appendWithSeparator(current, string(line)))
}

// decodeJournalLine parses one journal line.
func decodeJournalLine(line string) (JournalEntry, error) {
	var entry JournalEntry
	err := json.Unmarshal([]byte(line), &entry)
	return entry, err
}

// JournalStatus reports the journal's size and its last decodable entry.
// ok is false when no journal exists yet. The read takes the store mutex
// so it cannot interleave with a rotation.
func (s *Store) JournalStatus() (bytes int, last JournalEntry, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := readMemoryFile(JournalFile(s.projectDir))
	if err != nil || strings.TrimSpace(raw) == "" {
		return 0, JournalEntry{}, false
	}
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if entry, err := decodeJournalLine(lines[i]); err == nil {
			return len(raw), entry, true
		}
	}
	return len(raw), JournalEntry{}, true
}

// journalEntry builds a mutation record, clamping the summaries.
func journalEntry(op, key, oldDesc, newDesc, session string) JournalEntry {
	return JournalEntry{
		Op:      op,
		Key:     key,
		Old:     journalSummary(oldDesc),
		New:     journalSummary(newDesc),
		Session: session,
	}
}

// journalSummary collapses to one line and clamps to the entry budget.
func journalSummary(text string) string {
	return clampBytes(CollapseLine(text), journalSummaryMaxBytes)
}

// clampBytes cuts s at max bytes without splitting a UTF-8 rune.
func clampBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head, _ := HeadBytes(s, max)
	return head + "..."
}
