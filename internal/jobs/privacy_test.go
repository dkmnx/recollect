package jobs

// audit: completed job records do not retain raw model output containing private text.
// regression: a redacted fact could be reconstructed from the durable job's raw reply.
// gap: existing job tests inspect fact bytes, not the persisted job record.
// seam: Queue.Ingest, Next, and Complete over a temporary project with on-disk job state.

// audit: behavior - completed job records do not retain raw model output containing private text.
// audit: regression - a redacted fact could be reconstructed from the durable job raw reply.
// audit: coverage gap - existing job tests inspect fact bytes, not the persisted job record.
// audit: seam - Queue.Ingest, Next, and Complete over a temporary project with on-disk job state.
import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dkmnx/recollect/internal/memory"
)

func TestCompletedJobDropsRawOutput(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, store, projectDir, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-privacy")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	output := `{"ops":[{"action":"add","key":"privacy-job","summary":"note with <private>THE-RAW-SECRET</private> inside","content":"body"}],"topic":"t","summary":"s"}`
	if err := q.Complete(lease.ID, lease.Lease, output); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("privacy-job")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fact.Description, "THE-RAW-SECRET") {
		t.Fatalf("fact kept the raw secret: %q", fact.Description)
	}
	record, err := os.ReadFile(filepath.Join(projectDir, ".engine", "jobs", lease.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "THE-RAW-SECRET") {
		t.Fatalf("durable job kept the raw model output: %s", record)
	}
}

// audit: behavior - completed job records drop model output even when private markers are JSON escaped.
// audit: regression - a redacted fact was reconstructible from the escaped private text kept in the job record.
// audit: coverage gap - the first privacy test used literal markers that filterText could strip.
// audit: seam - Queue.Complete with an escaped private marker in the raw model reply.
func TestCompletedJobDropsEscapedOutput(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, store, projectDir, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-escaped")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	output := `{"ops":[{"action":"add","key":"escaped","summary":"note \u003cprivate\u003eESCAPED-SECRET\u003c/private\u003e inside","content":"body"}],"topic":"t","summary":"s"}`
	if err := q.Complete(lease.ID, lease.Lease, output); err != nil {
		t.Fatal(err)
	}
	fact, err := store.ReadFact("escaped")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fact.Description, "ESCAPED-SECRET") {
		t.Fatalf("fact kept the escaped secret: %q", fact.Description)
	}
	record, err := os.ReadFile(filepath.Join(projectDir, ".engine", "jobs", lease.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "ESCAPED-SECRET") {
		t.Fatalf("durable job kept escaped private text: %s", record)
	}
}

// audit: behavior - failed job records do not retain private text from a malformed model reply.
// audit: regression - reply excerpts redacted secrets but not private spans, so failure errors persisted them.
// audit: coverage gap - existing privacy tests only covered successfully completed jobs.
// audit: seam - Queue.Complete with a malformed reply containing a private span.
func TestFailedJobDropsPrivateTextFromError(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, _, projectDir, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-failed")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	if err := q.Complete(lease.ID, lease.Lease, "garbage <private>FAILED-SECRET</private> more"); err == nil {
		t.Fatal("malformed reply did not fail")
	}
	record, err := os.ReadFile(filepath.Join(projectDir, ".engine", "jobs", lease.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "FAILED-SECRET") {
		t.Fatalf("failed job kept private text: %s", record)
	}
}

// audit: behavior - failed job records persist only a stable code, never text from a malformed reply.
// audit: regression - JSON-escaped private markers survived both filters and reached durable job errors.
// audit: coverage gap - the earlier failure test used literal markers only.
// audit: seam - Queue.Complete with an escaped private marker in a malformed reply.
func TestFailedJobDropsEscapedReplyText(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, _, projectDir, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-escaped-failure")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	reply := `garbage \u003cprivate\u003eESCAPED-FAIL-SECRET\u003c/private\u003e more`
	if err := q.Complete(lease.ID, lease.Lease, reply); err == nil {
		t.Fatal("malformed reply did not fail")
	}
	record, err := os.ReadFile(filepath.Join(projectDir, ".engine", "jobs", lease.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(record), "ESCAPED-FAIL-SECRET") {
		t.Fatalf("failed job kept escaped reply text: %s", record)
	}
	var stored Job
	if err := json.Unmarshal(record, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Error != "INVALID_REPLY" {
		t.Fatalf("stored error code = %q, want INVALID_REPLY", stored.Error)
	}
}

// audit: behavior - a malformed reply stores its specific INVALID_REPLY code, not a generic IO code.
// audit: regression - stringifying the typed error before extraction collapsed it to MEMORY_IO.
// audit: coverage gap - the privacy tests checked text absence, not the stored code.
// audit: seam - Queue.Complete with a malformed reply and the persisted job record.
func TestFailedJobKeepsSpecificErrorCode(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, _, projectDir, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-code")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	if err := q.Complete(lease.ID, lease.Lease, "not json"); err == nil {
		t.Fatal("malformed reply did not fail")
	}
	record, err := os.ReadFile(filepath.Join(projectDir, ".engine", "jobs", lease.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var job Job
	if err := json.Unmarshal(record, &job); err != nil {
		t.Fatal(err)
	}
	if job.Error != memory.CodeInvalidReply {
		t.Fatalf("job error = %q, want %q", job.Error, memory.CodeInvalidReply)
	}
}
