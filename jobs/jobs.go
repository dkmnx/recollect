// Package jobs is the public facade over Recollect's durable work queue.
// The implementation lives in recollect/internal/jobs.
package jobs

import internaljobs "github.com/dkmnx/recollect/internal/jobs"

// Types shared between a host adapter and the queue.
type (
	HostRecord  = internaljobs.HostRecord
	Job         = internaljobs.Job
	Queue       = internaljobs.Queue
	RunBoundary = internaljobs.RunBoundary
)

// Status values a host adapter reports to its user.
const (
	StatePending   = internaljobs.StatePending
	StateLeased    = internaljobs.StateLeased
	StateApplied   = internaljobs.StateApplied
	StateRetryWait = internaljobs.StateRetryWait
	StateConflict  = internaljobs.StateConflict
	StateCancelled = internaljobs.StateCancelled
)

// Open binds a queue to a store and a private state directory.
var Open = internaljobs.Open
