package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"recollect/internal/fsatomic"
	"recollect/internal/memory"
	"recollect/internal/oslock"
)

const defaultLeaseTTL = 5 * time.Minute

// Queue persists and executes jobs over one memory store.
type Queue struct {
	mu          sync.Mutex
	store       *memory.Store
	dir         string
	generate    Generator
	now         func() time.Time
	harness     string
	lockTimeout time.Duration
}

// Open binds a queue to a store and a private state directory. Job files are
// written under <dir>/jobs; callers pass the project's .engine directory.
func Open(store *memory.Store, dir string, generate Generator) *Queue {
	return &Queue{
		store:       store,
		dir:         dir,
		generate:    generate,
		now:         time.Now,
		lockTimeout: 5 * time.Second,
	}
}

func (q *Queue) lockPath() string { return filepath.Join(q.dir, "jobs.lock") }

func (q *Queue) acquire() (func(), error) {
	release, err := oslock.Lock(q.lockPath(), q.lockTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: jobs: %v", ErrBusy, err)
	}
	return release, nil
}

// SetHarness sets the adapter identity recorded on new jobs.
func (q *Queue) SetHarness(id string) {
	q.mu.Lock()
	q.harness = id
	q.mu.Unlock()
}

func (q *Queue) jobsDir() string { return filepath.Join(q.dir, "jobs") }

func (q *Queue) jobPath(id string) string {
	return filepath.Join(q.jobsDir(), id+".json")
}

func (q *Queue) inputPath(id string) string {
	return filepath.Join(q.jobsDir(), id+".input.json")
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("jobs: create %s: %w", filepath.Dir(path), err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("jobs: temp in %s: %w", filepath.Dir(path), err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("jobs: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("jobs: close temp: %w", err)
	}
	if err := fsatomic.Replace(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("jobs: replace %s: %w", path, err)
	}
	return nil
}

func encode(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return append(data, '\n')
}

// writeJob writes only the public job record. Tests use it to place a job in
// a terminal state; normal mutations write the job and its input together.
func (q *Queue) writeJob(job Job) error {
	return writeAtomic(q.jobPath(job.ID), encode(job))
}

func (q *Queue) writePairLocked(job Job, input jobInput) error {
	if err := writeAtomic(q.inputPath(job.ID), encode(input)); err != nil {
		return err
	}
	return q.writeJob(job)
}

func (q *Queue) readPairLocked(id string) (Job, jobInput, bool, error) {
	data, err := os.ReadFile(q.jobPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return Job{}, jobInput{}, false, nil
	}
	if err != nil {
		return Job{}, jobInput{}, false, fmt.Errorf("jobs: read %s: %w", id, err)
	}
	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return Job{}, jobInput{}, false, fmt.Errorf("jobs: parse %s: %w", id, err)
	}
	var input jobInput
	if data, err := os.ReadFile(q.inputPath(id)); err == nil {
		if err := json.Unmarshal(data, &input); err != nil {
			return Job{}, jobInput{}, false, fmt.Errorf("jobs: parse input %s: %w", id, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Job{}, jobInput{}, false, fmt.Errorf("jobs: read input %s: %w", id, err)
	}
	return job, input, true, nil
}

func (q *Queue) readPair(id string) (Job, jobInput, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.readPairLocked(id)
}

func (q *Queue) listLocked() ([]Job, []jobInput, error) {
	entries, err := os.ReadDir(q.jobsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("jobs: list: %w", err)
	}
	type pair struct {
		job   Job
		input jobInput
	}
	var pairs []pair
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".input.json") {
			continue
		}
		job, input, ok, err := q.readPairLocked(strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, nil, err
		}
		if ok {
			pairs = append(pairs, pair{job: job, input: input})
		}
	}
	sort.SliceStable(pairs, func(i, j int) bool {
		if pairs[i].input.Seq != pairs[j].input.Seq {
			return pairs[i].input.Seq < pairs[j].input.Seq
		}
		return pairs[i].job.ID < pairs[j].job.ID
	})
	jobs := make([]Job, 0, len(pairs))
	inputs := make([]jobInput, 0, len(pairs))
	for _, p := range pairs {
		jobs = append(jobs, p.job)
		inputs = append(inputs, p.input)
	}
	return jobs, inputs, nil
}

func deterministicJob(job Job, input jobInput) bool {
	return job.Kind == KindRevision || (job.Kind == KindDigest && input.Fallback != "")
}

func checkLease(job Job, input jobInput, lease string, now time.Time) error {
	switch job.State {
	case StateCancelled:
		return ErrCancelled
	case StateLeased:
		if lease == "" || job.Lease != lease {
			return ErrInvalidLease
		}
		if !input.LeasedAt.IsZero() && !now.Before(input.LeasedAt.Add(defaultLeaseTTL)) {
			return ErrInvalidLease
		}
	case StatePending:
		if lease != "" || !deterministicJob(job, input) {
			return ErrInvalidLease
		}
	default:
		if lease == "" || job.Lease != lease {
			return ErrInvalidLease
		}
	}
	return nil
}

func recordsFor(records []HostRecord, boundary RunBoundary) []HostRecord {
	var out []HostRecord
	for _, record := range records {
		if record.SessionID == boundary.SessionID && record.Generation == boundary.Generation {
			out = append(out, record)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.Before(out[j].Time) })
	return out
}

// Ingest turns terminal runs into durable jobs. It never calls the generator.
func (q *Queue) Ingest(records []HostRecord, boundaries []RunBoundary) error {
	release, err := q.acquire()
	if err != nil {
		return err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := os.MkdirAll(q.jobsDir(), 0o755); err != nil {
		return fmt.Errorf("jobs: create state dir: %w", err)
	}
	maxSeq := 0
	_, inputs, err := q.listLocked()
	if err != nil {
		return err
	}
	for _, input := range inputs {
		if input.Seq > maxSeq {
			maxSeq = input.Seq
		}
	}
	seen := map[string]bool{}
	add := func(job Job, input jobInput) error {
		if seen[job.ID] {
			return nil
		}
		seen[job.ID] = true
		if _, err := os.Lstat(q.jobPath(job.ID)); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		maxSeq++
		input.Seq = maxSeq
		job.RequestID = job.ID
		job.InputHash = inputHash(job, input)
		return q.writePairLocked(job, input)
	}

	factList, err := q.store.ListFacts()
	if err != nil {
		return err
	}
	facts := factLines(factList)
	factCount := len(factList)

	var trigger *RunBoundary
	for _, boundary := range boundaries {
		window := selectWindow(recordsFor(records, boundary))
		first, last := recordSpan(window)
		base := Job{
			HarnessID:  q.harness,
			SessionID:  boundary.SessionID,
			Generation: boundary.Generation,
			Scope:      "project",
			State:      StatePending,
		}
		if !boundary.Available {
			gap := base
			gap.Kind = KindGap
			gap.Phase = "gap"
			gap.State = StateConflict
			gap.ID = jobID(gap.HarnessID, gap.SessionID, gap.Generation, gap.Phase, first, last)
			gap.Error = "unavailable history for generation " + boundary.Generation
			if err := add(gap, jobInput{}); err != nil {
				return err
			}
			continue
		}
		switch boundary.Kind {
		case "complete", "failed", "interrupted":
		default:
			continue
		}
		if trigger == nil {
			firstBoundary := boundary
			trigger = &firstBoundary
		}
		capturedOn := boundary.Time
		if len(window) > 0 && !window[0].Time.IsZero() {
			capturedOn = window[0].Time
		}
		firstRecord := capturedOn

		if boundary.Kind != "interrupted" {
			factJob := base
			factJob.Kind = KindFact
			factJob.Phase = "fact"
			factJob.ID = jobID(factJob.HarnessID, factJob.SessionID, factJob.Generation, factJob.Phase, first, last)
			factInput := jobInput{
				Prompt:     q.buildPrompt(KindFact, window, facts, nil),
				CapturedOn: firstRecord,
			}
			if err := add(factJob, factInput); err != nil {
				return err
			}
		}

		digestJob := base
		digestJob.Kind = KindDigest
		digestJob.Phase = "digest"
		digestJob.ID = jobID(digestJob.HarnessID, digestJob.SessionID, digestJob.Generation, digestJob.Phase, first, last)
		digestInput := jobInput{Prompt: q.buildPrompt(KindDigest, window, facts, nil), CapturedOn: firstRecord}
		if boundary.Kind == "failed" || boundary.Kind == "interrupted" {
			digestInput.Fallback = fallbackSummary(digestJob, len(window), boundary.Kind)
		}
		if err := add(digestJob, digestInput); err != nil {
			return err
		}

		revisionJob := base
		revisionJob.Kind = KindRevision
		revisionJob.Phase = "revision"
		revisionJob.ID = jobID(revisionJob.HarnessID, revisionJob.SessionID, revisionJob.Generation, revisionJob.Phase, first, last)
		revisions, unsupported := collectRevisions(window, revisionJob, q.now)
		revisionPayload, err := json.Marshal(revisions)
		if err != nil {
			return err
		}
		if err := add(revisionJob, jobInput{Payload: revisionPayload, CapturedOn: firstRecord}); err != nil {
			return err
		}
		for _, callID := range unsupported {
			gap := base
			gap.Kind = KindGap
			gap.Phase = "edit-gap"
			gap.State = StateConflict
			gap.ID = jobID(gap.HarnessID, gap.SessionID, gap.Generation, gap.Phase, callID, "")
			gap.Error = "unsupported edit evidence for tool call " + callID
			if err := add(gap, jobInput{}); err != nil {
				return err
			}
		}
	}

	if trigger == nil {
		return nil
	}
	makeMaintenance := func(kind Kind, phase string) error {
		job := Job{
			ID:         jobID(q.harness, trigger.SessionID, trigger.Generation, phase, "-", "-"),
			Kind:       kind,
			HarnessID:  q.harness,
			SessionID:  trigger.SessionID,
			Generation: trigger.Generation,
			Phase:      phase,
			Scope:      "project",
			State:      StatePending,
		}
		input := jobInput{Prompt: q.buildPrompt(kind, nil, facts, nil)}
		switch kind {
		case KindPages:
			var names []string
			for _, spec := range memory.PageCatalog() {
				names = append(names, spec.Name)
			}
			payload, err := json.Marshal(names)
			if err != nil {
				return err
			}
			input.Payload = payload
		}
		return add(job, input)
	}

	state, err := q.store.ReadCaptureState()
	if err != nil {
		return err
	}
	swept := q.now().Sub(state.LastEvolveAt) >= AutomaticCooldown
	if factCount >= AutomaticConsolidationFloor && swept {
		if err := makeMaintenance(KindConsolidate, "automatic"); err != nil {
			return err
		}
	} else if factCount >= ManualConsolidationFloor {
		if err := makeMaintenance(KindConsolidate, "manual"); err != nil {
			return err
		}
	}
	if factCount >= PagesFloor {
		if err := makeMaintenance(KindPages, "pages"); err != nil {
			return err
		}
	}
	return nil
}

// Next leases the oldest eligible pending job. A nil job means no work is
// currently available.
func (q *Queue) Next(harness, lease string) (*Job, error) {
	return q.NextCapable(harness, lease, true)
}

// NextCapable leases the oldest pending job the caller can execute. A caller
// without generation capability only receives deterministic work.
func (q *Queue) NextCapable(harness, lease string, generation bool) (*Job, error) {
	release, err := q.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, inputs, err := q.listLocked()
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].State != StatePending {
			continue
		}
		if jobs[i].HarnessID != harness {
			continue
		}
		if !deterministicJob(jobs[i], inputs[i]) && (!generation || q.generate == nil) {
			continue
		}
		if !jobs[i].RetryAt.IsZero() && q.now().Before(jobs[i].RetryAt) {
			continue
		}
		jobs[i].State = StateLeased
		jobs[i].Lease = lease
		jobs[i].Prompt = inputs[i].Prompt
		jobs[i].Deterministic = deterministicJob(jobs[i], inputs[i])
		inputs[i].LeasedAt = q.now()
		if err := q.writePairLocked(jobs[i], inputs[i]); err != nil {
			return nil, err
		}
		out := jobs[i]
		return &out, nil
	}
	return nil, nil
}

// Complete applies a leased result. A repeated completion returns the
// recorded result without mutating memory again.
func (q *Queue) Complete(id, lease, output string) error {
	release, err := q.acquire()
	if err != nil {
		return err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	job, input, ok, err := q.readPairLocked(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if job.State == StateApplied {
		return nil
	}
	if err := checkLease(job, input, lease, q.now()); err != nil {
		return err
	}
	if got := inputHash(job, input); got != job.InputHash {
		job.State = StateConflict
		job.Error = "input hash changed before completion"
		job.Lease = ""
		if err := q.writePairLocked(job, input); err != nil {
			return err
		}
		return ErrConflict
	}
	if err := q.applyOutput(job, input, output); err != nil {
		job.Attempt++
		job.State = StateRetryWait
		job.RetryAt = q.now().Add(defaultRetryCooldown)
		job.Error = err.Error()
		job.Lease = ""
		if err := q.writePairLocked(job, input); err != nil {
			return err
		}
		return err
	}
	job.Output = output
	job.Error = ""
	job.Lease = ""
	job.State = StateApplied
	input.LeasedAt = time.Time{}
	return q.writePairLocked(job, input)
}

// Fail records an attempt and a cooldown. It never advances a capture phase.
func (q *Queue) Fail(id, lease, reason string) error {
	release, err := q.acquire()
	if err != nil {
		return err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	job, input, ok, err := q.readPairLocked(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if job.State == StateApplied {
		return nil
	}
	if err := checkLease(job, input, lease, q.now()); err != nil {
		return err
	}
	job.Attempt++
	job.State = StateRetryWait
	job.RetryAt = q.now().Add(defaultRetryCooldown)
	job.Error = reason
	job.Lease = ""
	input.LeasedAt = time.Time{}
	return q.writePairLocked(job, input)
}

// Run executes one leased job with the host generator. Deterministic jobs
// such as revisions and fallbacks skip the model call.
func (q *Queue) Run(ctx context.Context, id, lease string) error {
	job, input, ok, err := q.readPair(id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	if err := checkLease(job, input, lease, q.now()); err != nil {
		return err
	}
	output := input.Fallback
	if output == "" && job.Kind != KindRevision {
		if q.generate == nil {
			return q.Fail(id, lease, ErrGenerationMissing.Error())
		}
		output, err = q.generate(ctx, input.Prompt)
		if err != nil {
			return q.Fail(id, lease, err.Error())
		}
	}
	return q.Complete(id, lease, output)
}

// Recover returns expired leases and elapsed cooldowns to pending.
func (q *Queue) Recover() error {
	release, err := q.acquire()
	if err != nil {
		return err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, inputs, err := q.listLocked()
	if err != nil {
		return err
	}
	now := q.now()
	for i := range jobs {
		changed := false
		if jobs[i].State == StateLeased && !inputs[i].LeasedAt.IsZero() &&
			!now.Before(inputs[i].LeasedAt.Add(defaultLeaseTTL)) {
			jobs[i].State = StatePending
			jobs[i].Lease = ""
			inputs[i].LeasedAt = time.Time{}
			changed = true
		}
		if jobs[i].State == StateRetryWait && !jobs[i].RetryAt.IsZero() && !now.Before(jobs[i].RetryAt) {
			jobs[i].State = StatePending
			jobs[i].RetryAt = time.Time{}
			changed = true
		}
		if changed {
			if err := q.writePairLocked(jobs[i], inputs[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// Status returns every job in creation order.
func (q *Queue) Status() ([]Job, error) {
	release, err := q.acquire()
	if err != nil {
		return nil, err
	}
	defer release()
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, _, err := q.listLocked()
	return jobs, err
}
