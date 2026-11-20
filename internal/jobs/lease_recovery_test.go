package jobs

// audit: behavior - recovery resets a leased job whose lease timestamp was cleared before the job record was replaced.
// audit: regression - a crash between the two durable writes left work leased forever and never re-leased.
// audit: coverage gap - existing recovery tests always write the job and input pair together.
// audit: seam - Queue.Next, a hand-built intermediate input file, Queue.Recover, and Queue.Next again.
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRecoverStrandedLeaseWithoutTimestamp(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, _, _, clock := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	leased, err := q.Next("opencode", "lease-1")
	if err != nil || leased == nil {
		t.Fatalf("Next = %+v, %v", leased, err)
	}

	data, err := os.ReadFile(q.inputPath(leased.ID))
	if err != nil {
		t.Fatal(err)
	}
	var input jobInput
	if err := json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	input.LeasedAt = time.Time{}
	if err := writeAtomic(q.inputPath(leased.ID), encode(input)); err != nil {
		t.Fatal(err)
	}

	clock.Advance(10 * defaultLeaseTTL)
	if err := q.Recover(); err != nil {
		t.Fatal(err)
	}
	after, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range after {
		if item.ID == leased.ID {
			if item.State != StatePending {
				t.Fatalf("stranded job state = %s, want pending", item.State)
			}
		}
	}
	next, err := q.Next("opencode", "lease-2")
	if err != nil || next == nil || next.ID != leased.ID {
		t.Fatalf("stranded job was not re-leased: next=%+v err=%v", next, err)
	}
}

// audit: behavior - replaying a completed revision job does not append its chain entries twice.
// audit: regression - recovery between apply and terminal state let a second completion duplicate revision lines.
// audit: coverage gap - existing revision tests complete a job only once.
// audit: seam - Queue.Complete, a simulated intermediate state, Queue.Recover, and a second Queue.Complete.
func TestRecoveredRevisionJobAppliesOnce(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	q, store, _, clock := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "edit-run.json"))
	revision := requireOne(t, q, KindRevision)
	q.mu.Lock()
	job, input, ok, err := q.readPairLocked(revision.ID)
	if !ok || err != nil {
		q.mu.Unlock()
		t.Fatalf("read revision pair: ok=%v err=%v", ok, err)
	}
	job.State = StateLeased
	job.Lease = "lease-revision"
	input.LeasedAt = clock.Now()
	err = q.writePairLocked(job, input)
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(revision.ID, "lease-revision", ""); err != nil {
		t.Fatal(err)
	}

	// The apply landed and the lease was cleared, but the terminal job state
	// never reached disk. Recovery must let a replay converge, not duplicate.
	q.mu.Lock()
	job, input, ok, err = q.readPairLocked(revision.ID)
	if !ok || err != nil {
		q.mu.Unlock()
		t.Fatalf("read intermediate pair: ok=%v err=%v", ok, err)
	}
	job.State = StateLeased
	input.LeasedAt = time.Time{}
	err = q.writePairLocked(job, input)
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Recover(); err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	job, input, ok, err = q.readPairLocked(revision.ID)
	if !ok || err != nil {
		q.mu.Unlock()
		t.Fatalf("read recovered pair: ok=%v err=%v", ok, err)
	}
	if job.State != StatePending {
		q.mu.Unlock()
		t.Fatalf("recovered state = %s, want pending", job.State)
	}
	job.State = StateLeased
	job.Lease = "lease-replay"
	input.LeasedAt = clock.Now()
	err = q.writePairLocked(job, input)
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(revision.ID, "lease-replay", ""); err != nil {
		t.Fatal(err)
	}
	revisions, err := store.ChainRevisions("main.go", -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 1 {
		t.Fatalf("chain revisions = %d, want 1", len(revisions))
	}
}

// audit: behavior - Fail keeps the host failure contract codes instead of folding them to MEMORY_IO.
// audit: regression - all non-cancelled reasons became MEMORY_IO, losing generation_timeout and peers.
// audit: coverage gap - existing Fail tests only check pending/conflict counts.
// audit: seam - Queue.Fail with each contract code over a leased job.
func TestFailKeepsContractCodes(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", nil }
	for _, code := range []string{"generation_timeout", "generation_failed", "generation_unavailable"} {
		q, _, _, _ := newTestQueue(t, gen)
		ingest(t, q, loadFixture(t, "completed-run.json"))
		lease, err := q.Next("opencode", "lease-"+code)
		if err != nil || lease == nil {
			t.Fatalf("Next = %+v, %v", lease, err)
		}
		if err := q.Fail(lease.ID, lease.Lease, code); err != nil {
			t.Fatal(err)
		}
		job, _, ok, err := q.readPair(lease.ID)
		if err != nil || !ok {
			t.Fatalf("read job: ok=%v err=%v", ok, err)
		}
		if job.Error != code {
			t.Fatalf("job error = %q, want %q", job.Error, code)
		}
	}
}

// audit: behavior - Run preserves a generator's generation_timeout failure code.
// audit: regression - FailErr mapped plain generator errors to MEMORY_IO.
// audit: coverage gap - no Run test used a generator that failed with a contract code.
// audit: seam - Queue.Run with a generator that returns a plain generation_timeout error.
func TestRunKeepsGeneratorFailureCode(t *testing.T) {
	gen := func(context.Context, string) (string, error) { return "", errors.New("generation_timeout") }
	q, _, _, _ := newTestQueue(t, gen)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	lease, err := q.Next("opencode", "lease-run")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	_ = q.Run(context.Background(), lease.ID, lease.Lease)
	job, _, ok, err := q.readPair(lease.ID)
	if err != nil || !ok {
		t.Fatalf("read job: ok=%v err=%v", ok, err)
	}
	if job.Error != "generation_timeout" {
		t.Fatalf("job error = %q, want generation_timeout", job.Error)
	}
}
