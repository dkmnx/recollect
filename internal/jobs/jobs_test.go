package jobs

// audit: behavior Stage 4 ingest produces idempotent fact, digest, revision,
// consolidation, page, and gap jobs from normalized host records; retry-wait
// jobs become leaseable after their cooldown, cancellations stay retryable,
// retries park at the attempt cap, compaction captures before the transcript
// shrinks, and idle creates no work.
// audit: regression covers lease and input-hash gating, unknown fact types,
// edit dedupe, fallback digests, retry cooldowns, and consolidation floors.
// audit: coverage gap E2E Queue API over a real memory.Store and on-disk state.
// audit: seam none; tests drive Open, Ingest, Next, Complete, Fail, Recover,
// Status and Run through a scripted host Generator without production hooks.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dkmnx/recollect/internal/memory"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type countingGenerator struct {
	mu    sync.Mutex
	calls int
	fn    func(ctx context.Context, prompt string) (string, error)
}

func (g *countingGenerator) Generate(ctx context.Context, prompt string) (string, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	return g.fn(ctx, prompt)
}

func (g *countingGenerator) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

func scripted(responses ...string) Generator {
	i := 0
	return func(ctx context.Context, prompt string) (string, error) {
		if i >= len(responses) {
			return "", fmt.Errorf("no more scripted responses")
		}
		out := responses[i]
		i++
		return out, nil
	}
}

type ingestFixture struct {
	Harness    string        `json:"harness"`
	Records    []HostRecord  `json:"records"`
	Boundaries []RunBoundary `json:"boundaries"`
}

func loadFixture(t *testing.T, name string) ingestFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var fixture ingestFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return fixture
}

func newTestQueue(t *testing.T, gen Generator) (*Queue, *memory.Store, string, *fakeClock) {
	t.Helper()
	projectDir := t.TempDir()
	clock := &fakeClock{t: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	store := memory.NewStore(memory.Options{
		UserDir:    t.TempDir(),
		ProjectDir: projectDir,
		Meta:       memory.ProjectMeta{Root: projectDir, Name: "proj", Slug: "proj"},
		Now:        clock.Now,
	})
	q := Open(store, filepath.Join(projectDir, ".engine"), gen)
	q.now = clock.Now
	return q, store, projectDir, clock
}

func ingest(t *testing.T, q *Queue, fixture ingestFixture) {
	t.Helper()
	q.SetHarness(fixture.Harness)
	if _, _, err := q.Ingest(fixture.Records, fixture.Boundaries); err != nil {
		t.Fatalf("ingest: %v", err)
	}
}

func jobsByKind(t *testing.T, q *Queue, kind Kind) []Job {
	t.Helper()
	all, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	var out []Job
	for _, job := range all {
		if job.Kind == kind {
			out = append(out, job)
		}
	}
	return out
}

func requireOne(t *testing.T, q *Queue, kind Kind) Job {
	t.Helper()
	jobs := jobsByKind(t, q, kind)
	if len(jobs) != 1 {
		t.Fatalf("%s jobs = %d, want 1: %+v", kind, len(jobs), jobs)
	}
	return jobs[0]
}

func leaseKind(t *testing.T, q *Queue, kind Kind, lease string) Job {
	t.Helper()
	for attempt := 0; attempt < 50; attempt++ {
		job, err := q.Next("opencode", lease)
		if err != nil {
			t.Fatal(err)
		}
		if job == nil {
			t.Fatalf("no pending job before %s", kind)
		}
		if job.Kind == kind {
			return *job
		}
		// Leave other jobs leased; they are not part of this scenario.
	}
	t.Fatalf("could not lease a %s job", kind)
	return Job{}
}

func leaseSpecific(t *testing.T, q *Queue, id, lease string) {
	t.Helper()
	job, input, ok, err := q.readPair(id)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	job.State = StateLeased
	job.Lease = lease
	input.LeasedAt = q.now()
	if err := q.writePairLocked(job, input); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedRunCreatesFactDigestRevisionJobs(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))

	if gen.count() != 0 {
		t.Fatalf("Ingest called the generator %d times, want 0", gen.count())
	}
	for _, kind := range []Kind{KindFact, KindDigest, KindRevision} {
		if jobs := jobsByKind(t, q, kind); len(jobs) == 0 {
			t.Fatalf("no %s job after a completed run", kind)
		}
	}

	job, err := q.Next("opencode", "lease-A")
	if err != nil || job == nil {
		t.Fatalf("Next = %+v, %v", job, err)
	}
	if job.State != StateLeased || job.Lease != "lease-A" {
		t.Fatalf("leased job = %+v", job)
	}
	if job.Kind != KindFact {
		t.Fatalf("first leased kind = %s, want fact", job.Kind)
	}

	output := `{"ops":[{"action":"add","key":"build-cmd","summary":"Build uses make check","content":"Run make check."}]}`
	if err := q.Complete(job.ID, "lease-A", output); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	fact, err := store.ReadFact("build-cmd")
	if err != nil || fact.Body != "Run make check." {
		t.Fatalf("applied fact = %+v (%v)", fact, err)
	}

	all, _ := q.Status()
	for _, item := range all {
		if item.ID == job.ID && item.State != StateApplied {
			t.Fatalf("completed job state = %s", item.State)
		}
		if item.Kind != KindFact && item.State != StatePending {
			t.Fatalf("%s job advanced with the fact phase: %s", item.Kind, item.State)
		}
	}
	if gen.count() != 0 {
		t.Fatalf("Complete called the generator, want 0")
	}
}

func TestFailedAndInterruptedCreateFallbackDigest(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "fallback-runs.json"))

	if gen.count() != 0 {
		t.Fatalf("Ingest called the generator %d times, want 0", gen.count())
	}
	digests := jobsByKind(t, q, KindDigest)
	if len(digests) != 2 {
		t.Fatalf("digest jobs = %d, want 2: %+v", len(digests), digests)
	}
	for _, i := range []int{0, 1} {
		job := leaseKind(t, q, KindDigest, fmt.Sprintf("fallback-lease-%d", i))
		if err := q.Run(context.Background(), job.ID, job.Lease); err != nil {
			t.Fatalf("Run fallback digest: %v", err)
		}
	}
	if gen.count() != 0 {
		t.Fatalf("fallback digests called the generator %d times, want 0", gen.count())
	}
	stored, err := store.ListDigests()
	if err != nil || len(stored) != 2 {
		t.Fatalf("stored digests = %d (%v), want 2", len(stored), err)
	}
	fallbackCount := 0
	for _, digest := range stored {
		if digest.Fallback && strings.Contains(digest.Summary, "session") {
			fallbackCount++
		}
	}
	if fallbackCount != 2 {
		t.Fatalf("fallback digests = %d, want 2: %+v", fallbackCount, stored)
	}
}

func TestMissingHistoryGapDoesNotBlockLaterRecords(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "gap-run.json"))

	gaps := jobsByKind(t, q, KindGap)
	if len(gaps) != 1 {
		t.Fatalf("gap jobs = %d, want 1", len(gaps))
	}
	if gaps[0].State == StatePending {
		t.Fatalf("gap job must not be pending: %+v", gaps[0])
	}
	if !strings.Contains(gaps[0].Error, "gen-1") {
		t.Fatalf("gap does not name the unavailable generation: %+v", gaps[0])
	}
	for _, kind := range []Kind{KindFact, KindDigest, KindRevision} {
		jobs := jobsByKind(t, q, kind)
		if len(jobs) == 0 || jobs[0].Generation != "gen-2" {
			t.Fatalf("%s jobs = %+v, want a gen-2 job", kind, jobs)
		}
	}
}

func TestRevisionDedupeAndFailedEdit(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "edit-run.json"))

	revision := requireOne(t, q, KindRevision)
	if err := q.Complete(revision.ID, "", ""); err != nil {
		t.Fatalf("Complete revision: %v", err)
	}
	revisions, err := store.ChainRevisions("main.go", -1)
	if err != nil || len(revisions) != 1 {
		t.Fatalf("chain revisions = %d (%v), want 1", len(revisions), err)
	}
	if revisions[0].NewText != "v2" || revisions[0].OldText != "v1" {
		t.Fatalf("revision = %+v, want v1 -> v2", revisions[0])
	}
	gaps := jobsByKind(t, q, KindGap)
	if len(gaps) != 1 || !strings.Contains(gaps[0].Error, "edit-3") {
		t.Fatalf("unsupported edit evidence gap = %+v", gaps)
	}
}

func TestUnknownFactTypePreservedAfterApply(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))

	factJob := requireOne(t, q, KindFact)
	leaseSpecific(t, q, factJob.ID, "lease-type")
	output := `{"ops":[{"action":"add","type":"custom-type","key":"custom","summary":"A custom typed fact","content":"body"}]}`
	if err := q.Complete(factJob.ID, "lease-type", output); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	fact, err := store.ReadFact("custom")
	if err != nil {
		t.Fatal(err)
	}
	if fact.Type != "custom-type" {
		t.Fatalf("fact type = %q, want custom-type", fact.Type)
	}
}

func seedFacts(t *testing.T, store *memory.Store, prefix string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("%s-fact-%d", prefix, i)
		if _, err := store.UpsertFact(memory.Fact{
			Name:        name,
			Description: fmt.Sprintf("%s description %d", prefix, i),
			Body:        fmt.Sprintf("%s body %d", prefix, i),
			Type:        memory.FactTypeProject,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConsolidationFloorsAndSeparatePages(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	seedFacts(t, store, "seed", 2)
	ingest(t, q, loadFixture(t, "completed-run.json"))

	manual := jobsByKind(t, q, KindConsolidate)
	if len(manual) != 1 || manual[0].Phase != "manual" {
		t.Fatalf("manual consolidation jobs = %+v", manual)
	}
	if jobs := jobsByKind(t, q, KindPages); len(jobs) != 0 {
		t.Fatalf("pages job exists below the floor: %+v", jobs)
	}

	seedFacts(t, store, "more", 6)
	second := loadFixture(t, "completed-run.json")
	second.Boundaries[0].Generation = "gen-2"
	for i := range second.Records {
		second.Records[i].Generation = "gen-2"
	}
	ingest(t, q, second)

	automatic := jobsByKind(t, q, KindConsolidate)
	if len(automatic) != 2 {
		t.Fatalf("consolidate jobs = %d, want 2: %+v", len(automatic), automatic)
	}
	pages := jobsByKind(t, q, KindPages)
	if len(pages) != 1 {
		t.Fatalf("pages jobs = %d, want 1: %+v", len(pages), pages)
	}

	var auto Job
	for _, job := range automatic {
		if job.Phase == "automatic" {
			auto = job
		}
	}
	leaseSpecific(t, q, auto.ID, "lease-auto")
	output := `{"ops":[{"action":"add","key":"invented","summary":"invented","content":"invented"},{"action":"drop","key":"seed-fact-0"}]}`
	if err := q.Complete(auto.ID, "lease-auto", output); err != nil {
		t.Fatalf("Complete consolidate: %v", err)
	}
	if _, err := store.ReadFact("invented"); err == nil {
		t.Fatal("consolidation invented a fact")
	}
	if _, err := store.ReadFact("seed-fact-0"); err == nil {
		t.Fatal("consolidation did not apply the drop")
	}

	leaseSpecific(t, q, pages[0].ID, "lease-pages")
	pageOutput := `{"pages":[{"name":"pitfalls","sources":"seed-fact-1","content":"Known traps in this project."}]}`
	if err := q.Complete(pages[0].ID, "lease-pages", pageOutput); err != nil {
		t.Fatalf("Complete pages: %v", err)
	}
	stored, err := store.ListPages()
	if err != nil || len(stored) != 1 || stored[0].Name != "pitfalls" {
		t.Fatalf("stored pages = %+v (%v)", stored, err)
	}
}

// audit: behavior - only the originating harness can lease a job.
// audit: regression - a foreign adapter would execute another harness's work.
// audit: coverage gap - harness identity was stored but not enforced at Next.
// audit: seam - the test calls Next with two harness IDs through the public API.
func TestWrongHarnessCannotLease(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	job, err := q.Next("foreign-harness", "lease-foreign")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if job != nil {
		t.Fatalf("foreign harness leased job %s", job.ID)
	}
}

// audit: behavior - manual consolidation leaves the automatic cooldown untouched.
// audit: regression - a 2-fact manual sweep would suppress an 8-fact sweep.
// audit: coverage gap - both sweeps wrote the same LastEvolveAt field.
// audit: seam - the test drives Complete on a real store and reads capture state.
func TestManualConsolidationDoesNotStampCooldown(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, _ := newTestQueue(t, gen.Generate)
	seedFacts(t, store, "seed", 2)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	var manual Job
	for _, job := range jobsByKind(t, q, KindConsolidate) {
		if job.Phase == "manual" {
			manual = job
		}
	}
	if manual.ID == "" {
		t.Fatal("no manual consolidation job")
	}
	leaseSpecific(t, q, manual.ID, "lease-manual")
	if err := q.Complete(manual.ID, "lease-manual", `{"ops":[]}`); err != nil {
		t.Fatalf("Complete manual: %v", err)
	}
	state, err := store.ReadCaptureState()
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastEvolveAt.IsZero() {
		t.Fatalf("manual sweep stamped LastEvolveAt: %v", state.LastEvolveAt)
	}
}

func TestLeaseGatingAndRepeatedCompletion(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, _, clock := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))

	factJob := requireOne(t, q, KindFact)
	wrong := `{"ops":[{"action":"add","key":"wrong","summary":"wrong lease fact","content":"x"}]}`
	if err := q.Complete(factJob.ID, "not-the-lease", wrong); err == nil {
		t.Fatal("wrong lease completed a job")
	}
	if _, err := store.ReadFact("wrong"); err == nil {
		t.Fatal("wrong lease mutated memory")
	}

	revision := requireOne(t, q, KindRevision)
	revision.State = StateCancelled
	if err := q.writeJob(revision); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(revision.ID, "", wrong); err == nil {
		t.Fatal("cancelled job accepted a result")
	}
	if _, err := store.ReadFact("wrong"); err == nil {
		t.Fatal("cancelled job mutated memory")
	}

	lease, err := q.Next("opencode", "lease-C")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	output := `{"ops":[{"action":"add","key":"right","summary":"right lease fact","content":"x"}]}`
	if err := q.Complete(lease.ID, lease.Lease, output); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, err := store.ReadFact("right"); err != nil {
		t.Fatalf("leased completion did not apply: %v", err)
	}
	var recorded string
	after, _ := q.Status()
	for _, item := range after {
		if item.ID == lease.ID {
			recorded = item.Output
		}
	}
	if err := q.Complete(lease.ID, "stale-lease", wrong); err != nil {
		t.Fatalf("repeated Complete should be idempotent: %v", err)
	}
	if _, err := store.ReadFact("wrong"); err == nil {
		t.Fatal("repeated completion mutated memory")
	}
	after, _ = q.Status()
	for _, item := range after {
		if item.ID == lease.ID && item.Output != recorded {
			t.Fatalf("recorded output changed: %q -> %q", recorded, item.Output)
		}
	}

	expired, err := q.Next("opencode", "lease-expired")
	if err != nil || expired == nil {
		t.Fatalf("Next = %+v, %v", expired, err)
	}
	clock.Advance(defaultLeaseTTL + time.Minute)
	if err := q.Recover(); err != nil {
		t.Fatal(err)
	}
	all, _ := q.Status()
	for _, item := range all {
		if item.ID == expired.ID {
			if item.State != StatePending {
				t.Fatalf("expired lease state = %s, want pending", item.State)
			}
		}
	}
	if err := q.Complete(expired.ID, "lease-expired", `{"topic":"t","summary":"a summary long enough"}`); err == nil {
		t.Fatal("expired lease was accepted after recovery")
	}
}

// audit: behavior - an expired lease is rejected without a recovery pass.
// audit: regression - a slow worker could apply a stale result after TTL.
// audit: coverage gap - the old test invoked Recover and hid the expiry check.
// audit: seam - the test advances the queue clock and calls Complete directly.
func TestExpiredLeaseRejectedWithoutRecover(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, clock := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	job, err := q.Next("opencode", "lease-exp")
	if err != nil || job == nil {
		t.Fatalf("Next = %+v, %v", job, err)
	}
	clock.Advance(defaultLeaseTTL + time.Second)
	if err := q.Complete(job.ID, "lease-exp", `{"ops":[]}`); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("expired lease: got %v, want invalid_lease", err)
	}
}

// audit: behavior - a pending model job cannot be completed without a lease.
// audit: regression - any caller could inject a result into pending work.
// audit: coverage gap - previous tests only exercised deterministic pending jobs.
// audit: seam - the test calls Complete on a freshly ingested fact job.
func TestPendingModelJobNeedsLease(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	factJob := requireOne(t, q, KindFact)
	if err := q.Complete(factJob.ID, "", `{"ops":[]}`); !errors.Is(err, ErrInvalidLease) {
		t.Fatalf("unleased completion: got %v, want invalid_lease", err)
	}
}

// audit: behavior - changing a frozen sidecar before completion is a conflict.
// audit: regression - a captured date or fallback could change after leasing.
// audit: coverage gap - input hash previously omitted Fallback and CapturedOn.
// audit: seam - the test edits the on-disk sidecar through the package API.
func TestSidecarTamperIsRejected(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, _ := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))
	factJob := requireOne(t, q, KindFact)
	leaseSpecific(t, q, factJob.ID, "lease-tamper")
	job, input, ok, err := q.readPair(factJob.ID)
	if err != nil || !ok {
		t.Fatalf("read pair: %v, ok=%v", err, ok)
	}
	input.CapturedOn = input.CapturedOn.Add(time.Hour)
	if err := q.writePairLocked(job, input); err != nil {
		t.Fatal(err)
	}
	if err := q.Complete(factJob.ID, "lease-tamper", `{"ops":[]}`); !errors.Is(err, ErrConflict) {
		t.Fatalf("tampered sidecar: got %v, want conflict", err)
	}
}

func TestStateSurvivesReopenAndRecover(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, store, projectDir, clock := newTestQueue(t, gen.Generate)
	ingest(t, q, loadFixture(t, "completed-run.json"))

	lease, err := q.Next("opencode", "lease-reopen")
	if err != nil || lease == nil {
		t.Fatalf("Next = %+v, %v", lease, err)
	}
	if err := q.Complete(lease.ID, lease.Lease,
		`{"ops":[{"action":"add","key":"persisted","summary":"persisted fact","content":"x"}]}`); err != nil {
		t.Fatal(err)
	}
	clock.Advance(defaultLeaseTTL + time.Minute)

	reopened := Open(store, filepath.Join(projectDir, ".engine"), gen.Generate)
	reopened.now = clock.Now
	if err := reopened.Recover(); err != nil {
		t.Fatal(err)
	}
	all, err := reopened.Status()
	if err != nil {
		t.Fatal(err)
	}
	foundApplied := false
	for _, job := range all {
		if job.ID == lease.ID {
			if job.State != StateApplied {
				t.Fatalf("applied job regressed to %s after reopen", job.State)
			}
			foundApplied = true
		}
	}
	if !foundApplied {
		t.Fatal("applied job missing after reopen")
	}
	if _, err := store.ReadFact("persisted"); err != nil {
		t.Fatalf("applied fact missing after reopen: %v", err)
	}
}

func TestIngestIsIdempotent(t *testing.T) {
	gen := &countingGenerator{fn: scripted()}
	q, _, _, _ := newTestQueue(t, gen.Generate)
	fixture := loadFixture(t, "completed-run.json")
	ingest(t, q, fixture)
	before, _ := q.Status()
	ingest(t, q, fixture)
	after, _ := q.Status()
	if len(before) != len(after) {
		t.Fatalf("repeated ingest changed job count: %d -> %d", len(before), len(after))
	}
}

// audit: behavior - two processes cannot lease the same pending job.
// audit: regression - a second worker would apply the same output twice.
// audit: coverage gap - the in-process mutex does not cross process boundaries.
// audit: seam - child processes use only the public Open and Next API.
func TestTwoProcessesCannotLeaseSameJob(t *testing.T) {
	q, _, projectDir, _ := newTestQueue(t, nil)
	q.SetHarness("opencode")
	ingest(t, q, loadFixture(t, "completed-run.json"))
	all, err := q.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("fixture produced %d jobs, want at least two", len(all))
	}
	for _, job := range all[1:] {
		done := job
		done.State = StateApplied
		if err := q.writeJob(done); err != nil {
			t.Fatalf("settle job: %v", err)
		}
	}

	userDir := filepath.Join(projectDir, "user")
	out := make(chan string, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=TestHelperLeaseWorker")
			cmd.Env = append(os.Environ(),
				"RECOLLECT_JOBS_HELPER=1",
				"RECOLLECT_JOBS_DIR="+filepath.Join(projectDir, ".engine"),
				"RECOLLECT_PROJECT_DIR="+projectDir,
				"RECOLLECT_USER_DIR="+userDir,
			)
			data, err := cmd.CombinedOutput()
			if err != nil {
				out <- "error:" + err.Error() + string(data)
				return
			}
			out <- strings.TrimSpace(string(data))
		}()
	}
	wg.Wait()
	close(out)
	var results []string
	for result := range out {
		if strings.HasPrefix(result, "error:") {
			t.Fatalf("worker failed: %s", result)
		}
		results = append(results, result)
	}
	jobIDPattern := regexp.MustCompile(`(?m)^[0-9a-f]{32}$`)
	leased := 0
	for _, result := range results {
		if jobIDPattern.MatchString(result) {
			leased++
		}
	}
	if leased != 1 {
		t.Fatalf("leased jobs = %d, want exactly one", leased)
	}
}

func TestRetryWaitJobBecomesLeaseableAfterCooldown(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	q.SetHarness("opencode")
	if _, _, err := q.Ingest(nil, []RunBoundary{{
		SessionID: "s1", Generation: "g1", Kind: "complete", Time: clock.Now(), Available: true,
	}}); err != nil {
		t.Fatal(err)
	}
	job, err := q.Next("opencode", "lease-1")
	if err != nil || job == nil {
		t.Fatalf("first lease: job=%+v err=%v", job, err)
	}
	if err := q.Fail(job.ID, "lease-1", "generation_timeout"); err != nil {
		t.Fatal(err)
	}
	if got, err := q.Next("opencode", "lease-2"); err != nil || got != nil {
		t.Fatalf("future retry leased: job=%+v err=%v", got, err)
	}
	clock.Advance(2 * time.Minute)
	got, err := q.Next("opencode", "lease-3")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != job.ID || got.State != StateLeased {
		t.Fatalf("elapsed retry not leased: %+v", got)
	}
}

func TestNextRecoversExpiredLease(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	job := Job{
		ID:                   "expired-lease",
		HarnessID:            "opencode",
		Kind:                 KindRevision,
		State:                StateLeased,
		Lease:                "old-lease",
		PrivacyPolicyVersion: privacyPolicyVersion1,
	}
	q.mu.Lock()
	err := q.writePairLocked(job, jobInput{LeasedAt: clock.Now()})
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(defaultLeaseTTL + time.Minute)
	got, err := q.Next("opencode", "lease-new")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.ID != job.ID || got.State != StateLeased {
		t.Fatalf("expired lease not recovered: %+v", got)
	}
}

func TestIdleBoundaryCreatesNoWork(t *testing.T) {
	q, _, _, _ := newTestQueue(t, nil)
	q.SetHarness("opencode")
	_, _, err := q.Ingest(nil, []RunBoundary{
		{SessionID: "ses-1", Generation: "g1", Kind: "terminal", Available: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	all, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("idle boundary created %d jobs: %+v", len(all), all)
	}
}

func TestExpiredLeaseParksAtAttemptCap(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	job := Job{
		ID:                   "expired-cap",
		HarnessID:            "opencode",
		Kind:                 KindRevision,
		State:                StateLeased,
		Lease:                "lease-0",
		PrivacyPolicyVersion: privacyPolicyVersion1,
	}
	q.mu.Lock()
	err := q.writePairLocked(job, jobInput{LeasedAt: clock.Now()})
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= maxJobAttempts; attempt++ {
		clock.Advance(defaultLeaseTTL + time.Minute)
		leased, err := q.Next("opencode", fmt.Sprintf("lease-%d", attempt))
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		if attempt < maxJobAttempts && (leased == nil || leased.ID != job.ID) {
			t.Fatalf("attempt %d next: job=%+v", attempt, leased)
		}
		if attempt == maxJobAttempts && leased != nil {
			t.Fatalf("capped job leased again: %+v", leased)
		}
	}
	all, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].State != StateConflict {
		t.Fatalf("expired-lease cap state = %+v", all)
	}
}

func TestCancelledJobStaysRetryable(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	job := Job{
		ID:                   "cancel-retry",
		HarnessID:            "opencode",
		Kind:                 KindRevision,
		State:                StateLeased,
		Lease:                "lease-0",
		PrivacyPolicyVersion: privacyPolicyVersion1,
	}
	q.mu.Lock()
	err := q.writePairLocked(job, jobInput{LeasedAt: clock.Now()})
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	lease := "lease-0"
	for attempt := 1; attempt <= maxJobAttempts; attempt++ {
		if err := q.Fail(job.ID, lease, "cancelled"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		clock.Advance(time.Hour)
		if attempt == maxJobAttempts {
			break
		}
		leased, err := q.Next("opencode", fmt.Sprintf("lease-%d", attempt))
		if err != nil || leased == nil || leased.ID != job.ID {
			t.Fatalf("attempt %d next: job=%+v err=%v", attempt, leased, err)
		}
		lease = leased.Lease
	}
	if got, err := q.Next("opencode", "after-cancel"); err != nil || got == nil || got.ID != job.ID {
		t.Fatalf("cancelled job is no longer retryable: job=%+v err=%v", got, err)
	}
}

func TestCompactionBoundaryCapturesBeforeHistoryChanges(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	q.SetHarness("opencode")
	records := []HostRecord{{
		ID: "m1", SessionID: "ses-1", Generation: "g1", Role: "user", Text: "pre-shrink text", Time: clock.Now(),
	}}
	if _, _, err := q.Ingest(records, []RunBoundary{{
		SessionID: "ses-1", Generation: "g1", Kind: "compaction", Time: clock.Now(), Available: true,
	}}); err != nil {
		t.Fatal(err)
	}
	factJobs := jobsByKind(t, q, KindFact)
	if len(factJobs) != 1 {
		t.Fatalf("compaction fact jobs = %d, want 1", len(factJobs))
	}
	_, input, ok, err := q.readPair(factJobs[0].ID)
	if err != nil || !ok {
		t.Fatalf("read fact input: ok=%v err=%v", ok, err)
	}
	if !strings.Contains(input.Prompt, "pre-shrink text") {
		t.Fatalf("compaction prompt lost pre-shrink history: %q", input.Prompt)
	}
	if got := jobsByKind(t, q, KindDigest); len(got) != 1 {
		t.Fatalf("compaction digest jobs = %d, want 1", len(got))
	}
}

func TestUnavailableCompactionCreatesGap(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	q.SetHarness("opencode")
	if _, _, err := q.Ingest(nil, []RunBoundary{{
		SessionID: "ses-1", Generation: "g1", Kind: "compaction", Time: clock.Now(), Available: false,
	}}); err != nil {
		t.Fatal(err)
	}
	if got := jobsByKind(t, q, KindGap); len(got) != 1 {
		t.Fatalf("unavailable compaction gap jobs = %d, want 1", len(got))
	}
}

func TestFailParksJobAfterMaxAttempts(t *testing.T) {
	q, _, _, clock := newTestQueue(t, nil)
	job := Job{
		ID:                   "retry-cap",
		HarnessID:            "opencode",
		Kind:                 KindRevision,
		State:                StateLeased,
		Lease:                "lease-0",
		PrivacyPolicyVersion: privacyPolicyVersion1,
	}
	q.mu.Lock()
	err := q.writePairLocked(job, jobInput{LeasedAt: clock.Now()})
	q.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	lease := "lease-0"
	for attempt := 1; attempt <= maxJobAttempts; attempt++ {
		if err := q.Fail(job.ID, lease, "generation_failed"); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		clock.Advance(time.Hour)
		if attempt == maxJobAttempts {
			break
		}
		leased, err := q.Next("opencode", fmt.Sprintf("lease-%d", attempt))
		if err != nil || leased == nil || leased.ID != job.ID {
			t.Fatalf("attempt %d next: job=%+v err=%v", attempt, leased, err)
		}
		lease = leased.Lease
	}
	if got, err := q.Next("opencode", "after-cap"); err != nil || got != nil {
		t.Fatalf("capped job leased again: job=%+v err=%v", got, err)
	}
	all, err := q.Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].State != StateConflict {
		t.Fatalf("capped job state = %+v", all)
	}
}

func TestHelperLeaseWorker(t *testing.T) {
	if os.Getenv("RECOLLECT_JOBS_HELPER") != "1" {
		return
	}
	projectDir := os.Getenv("RECOLLECT_PROJECT_DIR")
	store := memory.NewStore(memory.Options{
		ProjectDir: projectDir,
		UserDir:    os.Getenv("RECOLLECT_USER_DIR"),
	})
	q := Open(store, os.Getenv("RECOLLECT_JOBS_DIR"), func(context.Context, string) (string, error) {
		return "", nil
	})
	q.SetHarness("opencode")
	job, err := q.Next("opencode", "shared-lease")
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if job != nil {
		fmt.Println(job.ID)
	}
}
