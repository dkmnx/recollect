package jobs

// audit: behavior - the background runner completes deterministic jobs, generates for model jobs, and maps failures.
// audit: regression - a stalled or unavailable generator could wedge the lease loop or lose the job.
// audit: coverage gap - the worker semantics lived only in the TypeScript adapter.
// audit: seam - the test drives Step with a fake queue so no files or processes are involved.

import (
	"context"
	"testing"
	"time"
)

type fakeQueue struct {
	job       *Job
	completed []string
	failed    []string
}

func (q *fakeQueue) NextCapable(_, _ string, _ bool) (*Job, error) {
	job := q.job
	q.job = nil
	return job, nil
}

func (q *fakeQueue) Complete(id, _, output string) error {
	q.completed = append(q.completed, id+":"+output)
	return nil
}

func (q *fakeQueue) Fail(id, _, reason string) error {
	q.failed = append(q.failed, id+":"+reason)
	return nil
}

func TestCapTimeoutStaysWithinLease(t *testing.T) {
	if got := capTimeout(30 * time.Second); got != 30*time.Second {
		t.Fatalf("short timeout = %s", got)
	}
	if got := capTimeout(0); got != MaxGenerationTimeout {
		t.Fatalf("zero timeout = %s, want %s", got, MaxGenerationTimeout)
	}
	if got := capTimeout(time.Hour); got != MaxGenerationTimeout {
		t.Fatalf("long timeout = %s, want %s", got, MaxGenerationTimeout)
	}
}

func TestRunnerCompletesDeterministicJob(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "det", Deterministic: true}}
	runner := &Runner{Queue: queue}
	worked, err := runner.Step(context.Background())
	if err != nil || !worked {
		t.Fatalf("worked = %v, err = %v", worked, err)
	}
	if len(queue.completed) != 1 || queue.completed[0] != "det:" {
		t.Fatalf("completed = %v", queue.completed)
	}
}

func TestRunnerGeneratesForModelJob(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "gen", Prompt: "hello"}}
	runner := &Runner{
		Queue: queue, Generation: true, Timeout: time.Second,
		Generate: func(context.Context, string) (string, error) { return "world", nil },
	}
	worked, err := runner.Step(context.Background())
	if err != nil || !worked {
		t.Fatalf("worked = %v, err = %v", worked, err)
	}
	if len(queue.completed) != 1 || queue.completed[0] != "gen:world" {
		t.Fatalf("completed = %v", queue.completed)
	}
}

func TestRunnerTimeoutMapsReason(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "slow"}}
	runner := &Runner{
		Queue: queue, Generation: true, Timeout: 10 * time.Millisecond,
		Generate: func(ctx context.Context, _ string) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		},
	}
	if _, err := runner.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.failed) != 1 || queue.failed[0] != "slow:generation_timeout" {
		t.Fatalf("failed = %v", queue.failed)
	}
}

func TestRunnerReleasesLeaseWhenCanceled(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "cancel"}}
	runner := &Runner{Queue: queue}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	worked, err := runner.Step(ctx)
	if err != nil || !worked {
		t.Fatalf("worked = %v, err = %v", worked, err)
	}
	if len(queue.failed) != 1 || queue.failed[0] != "cancel:cancelled" {
		t.Fatalf("failed = %v", queue.failed)
	}
	if len(queue.completed) != 0 {
		t.Fatalf("completed = %v", queue.completed)
	}
}

func TestRunnerReleasesLeaseWhenCanceledAfterGenerate(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "late"}}
	ctx, cancel := context.WithCancel(context.Background())
	runner := &Runner{
		Queue: queue, Generation: true, Timeout: time.Second,
		Generate: func(context.Context, string) (string, error) {
			cancel()
			return "late output", nil
		},
	}
	if _, err := runner.Step(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queue.failed) != 1 || queue.failed[0] != "late:cancelled" {
		t.Fatalf("failed = %v", queue.failed)
	}
	if len(queue.completed) != 0 {
		t.Fatalf("completed = %v", queue.completed)
	}
}

func TestRunnerWithoutGeneratorFailsUnavailable(t *testing.T) {
	queue := &fakeQueue{job: &Job{ID: "nog"}}
	runner := &Runner{Queue: queue, Generation: true}
	if _, err := runner.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.failed) != 1 || queue.failed[0] != "nog:generation_unavailable" {
		t.Fatalf("failed = %v", queue.failed)
	}
}
