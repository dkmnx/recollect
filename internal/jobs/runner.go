package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"
)

const (
	runnerIdle      = 1500 * time.Millisecond
	runnerRetryBase = time.Second
	runnerRetryMax  = 30 * time.Second
)

// MaxGenerationTimeout leaves a minute to persist a result before the lease
// expires. A longer model call would complete against an invalid lease.
const MaxGenerationTimeout = defaultLeaseTTL - time.Minute

func capTimeout(requested time.Duration) time.Duration {
	if requested <= 0 || requested > MaxGenerationTimeout {
		return MaxGenerationTimeout
	}
	return requested
}

// RunnerQueue is the queue surface the background runner needs.
type RunnerQueue interface {
	NextCapable(harness, lease string, generation bool) (*Job, error)
	Complete(id, lease, output string) error
	Fail(id, lease, reason string) error
}

// Runner leases jobs for one harness and completes them with an injected
// generator. It mirrors the OpenCode adapter worker.
type Runner struct {
	Queue      RunnerQueue
	Harness    string
	Generation bool
	Timeout    time.Duration
	Idle       time.Duration
	Generate   func(context.Context, string) (string, error)
	Log        func(error)
}

// Run polls until the context is canceled.
func (r *Runner) Run(ctx context.Context) {
	retry := time.Duration(0)
	for {
		if ctx.Err() != nil {
			return
		}
		worked, err := r.Step(ctx)
		delay := r.Idle
		if delay <= 0 {
			delay = runnerIdle
		}
		if err != nil {
			if retry == 0 {
				retry = runnerRetryBase
			} else {
				retry *= 2
				if retry > runnerRetryMax {
					retry = runnerRetryMax
				}
			}
			delay = retry
			if r.Log != nil {
				r.Log(err)
			}
		} else if worked {
			retry = 0
		}
		if !sleep(ctx, delay) {
			return
		}
	}
}

// Step leases and handles at most one job.
func (r *Runner) Step(ctx context.Context) (bool, error) {
	lease, err := leaseToken()
	if err != nil {
		return false, err
	}
	job, err := r.Queue.NextCapable(r.Harness, lease, r.Generation)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return true, r.Queue.Fail(job.ID, lease, "cancelled")
	}
	if job.Deterministic {
		return true, r.Queue.Complete(job.ID, lease, "")
	}
	if !r.Generation || r.Generate == nil {
		return true, r.Queue.Fail(job.ID, lease, ErrGenerationMissing.Error())
	}
	runCtx, cancel := context.WithTimeout(ctx, capTimeout(r.Timeout))
	defer cancel()
	output, err := r.Generate(runCtx, job.Prompt)
	if err != nil {
		reason := "generation_failed"
		switch {
		case runCtx.Err() == context.DeadlineExceeded:
			reason = "generation_timeout"
		case ctx.Err() != nil:
			reason = "cancelled"
		}
		return true, r.Queue.Fail(job.ID, lease, reason)
	}
	if ctx.Err() != nil {
		return true, r.Queue.Fail(job.ID, lease, "cancelled")
	}
	return true, r.Queue.Complete(job.ID, lease, output)
}

func sleep(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func leaseToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", errors.New("lease_unavailable")
	}
	return hex.EncodeToString(buf[:]), nil
}
