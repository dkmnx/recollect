package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

// Capture and maintenance thresholds. Manual and automatic sweeps keep the
// tested floors; page maintenance has its own floor and never rides a sweep.
const (
	ManualConsolidationFloor    = 2
	AutomaticConsolidationFloor = 8
	PagesFloor                  = 8
	AutomaticCooldown           = 24 * time.Hour

	defaultRetryCooldown  = time.Minute
	captureWindowMessages = 24
	captureWindowBytes    = 16 * 1024
)

// jobInput is private work beside a Job. It holds the frozen prompt and
// payload, the lease timestamp, and the deterministic digest fallback.
type jobInput struct {
	Seq        int             `json:"seq"`
	Prompt     string          `json:"prompt,omitempty"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	LeasedAt   time.Time       `json:"leasedAt,omitempty"`
	CapturedOn time.Time       `json:"capturedOn,omitempty"`
	Fallback   string          `json:"fallback,omitempty"`
}

func jobID(harness, session, generation, phase, first, last string) string {
	key := strings.Join([]string{harness, session, generation, phase, first, last}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

type hashMaterial struct {
	Kind       Kind   `json:"kind"`
	HarnessID  string `json:"harnessID"`
	SessionID  string `json:"sessionID"`
	Generation string `json:"generation"`
	Phase      string `json:"phase"`
	Scope      string `json:"scope"`
	Prompt     string `json:"prompt"`
	Payload    string `json:"payload"`
	CapturedOn string `json:"capturedOn,omitempty"`
	Fallback   string `json:"fallback,omitempty"`
}

// canonicalPayload removes layout differences RawMessage picks up when the
// sidecar is written with indentation. The hash guards bytes, not formatting.
func canonicalPayload(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, raw); err != nil {
		return string(raw)
	}
	return compacted.String()
}

func inputHash(job Job, input jobInput) string {
	material := hashMaterial{
		Kind:       job.Kind,
		HarnessID:  job.HarnessID,
		SessionID:  job.SessionID,
		Generation: job.Generation,
		Phase:      job.Phase,
		Scope:      job.Scope,
		Prompt:     input.Prompt,
		Payload:    canonicalPayload(input.Payload),
		CapturedOn: input.CapturedOn.UTC().Format(time.RFC3339Nano),
		Fallback:   input.Fallback,
	}
	data, err := json.Marshal(material)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func recordSpan(records []HostRecord) (string, string) {
	if len(records) == 0 {
		return "-", "-"
	}
	return records[0].ID, records[len(records)-1].ID
}

// selectWindow keeps the newest messages under both bounds. User messages are
// dropped only after every non-user record has gone.
func selectWindow(records []HostRecord) []HostRecord {
	start := 0
	if len(records) > captureWindowMessages {
		start = len(records) - captureWindowMessages
	}
	window := append([]HostRecord(nil), records[start:]...)
	size := func() int {
		total := 0
		for _, record := range window {
			total += len(record.Text)
		}
		return total
	}
	for size() > captureWindowBytes {
		victim := -1
		for i := range window {
			if window[i].Role != "user" {
				victim = i
				break
			}
		}
		if victim < 0 && len(window) > 0 {
			victim = 0
		}
		if victim < 0 {
			break
		}
		window = append(window[:victim], window[victim+1:]...)
	}
	return window
}

func windowText(records []HostRecord) string {
	var b strings.Builder
	for _, record := range records {
		role := record.Role
		if role == "" {
			role = "unknown"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(record.Text))
		b.WriteByte('\n')
	}
	return b.String()
}

func namespacedSession(job Job) string {
	if job.HarnessID == "" {
		return job.SessionID
	}
	return job.HarnessID + ":" + job.SessionID
}

type editEvidence struct {
	Path    string `json:"path"`
	Old     string `json:"oldString"`
	New     string `json:"newString"`
	OldAlt  string `json:"old"`
	NewAlt  string `json:"new"`
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

func parseEditEvidence(raw json.RawMessage) (path, old, newText string, ok bool) {
	var evidence editEvidence
	if len(raw) == 0 || json.Unmarshal(raw, &evidence) != nil {
		return "", "", "", false
	}
	path = strings.TrimSpace(evidence.Path)
	for _, candidate := range []string{evidence.Old, evidence.OldAlt, evidence.OldText} {
		if old == "" {
			old = candidate
		}
	}
	for _, candidate := range []string{evidence.New, evidence.NewAlt, evidence.NewText} {
		if newText == "" {
			newText = candidate
		}
	}
	ok = path != "" && old != "" && newText != ""
	return path, old, newText, ok
}

func isEditCall(call ToolCall) bool {
	name := strings.ToLower(call.Name)
	class := strings.ToLower(call.Class)
	return strings.Contains(name, "edit") || strings.Contains(class, "edit") || class == "workspace_mutation"
}

func isSuccessOutcome(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "success", "ok", "applied", "done":
		return true
	}
	return false
}

func isFailedOutcome(outcome string) bool {
	value := strings.ToLower(strings.TrimSpace(outcome))
	return strings.Contains(value, "fail") || strings.Contains(value, "error") || strings.Contains(value, "reject")
}

// collectRevisions extracts one chain entry per successful edit event. The
// IDs of unsupported successful edits come back so Ingest can record a gap.
func collectRevisions(records []HostRecord, job Job, now func() time.Time) ([]memory.ChainRevision, []string) {
	seen := map[string]bool{}
	var revisions []memory.ChainRevision
	var unsupported []string
	for _, record := range records {
		for _, call := range record.ToolCalls {
			if !isEditCall(call) || seen[call.ID] {
				continue
			}
			if isFailedOutcome(call.Outcome) {
				seen[call.ID] = true
				continue
			}
			if !isSuccessOutcome(call.Outcome) {
				continue
			}
			seen[call.ID] = true
			path, old, newText, ok := parseEditEvidence(call.EditEvidence)
			if !ok {
				if len(call.EditEvidence) > 0 {
					unsupported = append(unsupported, call.ID)
				}
				continue
			}
			at := record.Time
			if at.IsZero() {
				at = now()
			}
			revisions = append(revisions, memory.ChainRevision{
				Path:      path,
				SessionID: namespacedSession(job),
				At:        at,
				OldText:   old,
				NewText:   newText,
			})
		}
	}
	return revisions, unsupported
}

func fallbackSummary(job Job, recordCount int, boundaryKind string) string {
	return "session " + job.SessionID + " generation " + job.Generation +
		" ended " + boundaryKind + ". Captured " + strconv.Itoa(recordCount) +
		" records; no model digest is available."
}

func factLines(facts []memory.Fact) string {
	var b strings.Builder
	for _, fact := range facts {
		b.WriteString(fact.Name)
		b.WriteString(": ")
		b.WriteString(fact.Description)
		b.WriteByte('\n')
	}
	return b.String()
}
