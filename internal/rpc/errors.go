package rpc

import (
	"errors"

	"recollect/internal/engine"
	"recollect/internal/jobs"
	"recollect/internal/memory"
)

// engineFailure maps a stable engine error to a JSON-RPC error.
func engineFailure(err error) *Error {
	switch {
	case errors.Is(err, engine.ErrReadRequired):
		return failure(CodeReadRequired, "read_required")
	case errors.Is(err, engine.ErrRevisionConflict):
		return failure(CodeRevision, "revision_conflict")
	case errors.Is(err, engine.ErrBusy):
		return failure(CodeBusy, "busy")
	case errors.Is(err, engine.ErrConflict):
		return failure(CodeConflict, "conflict")
	case errors.Is(err, engine.ErrNotFound), memory.CodeOf(err) == memory.CodeFactNotFound:
		return invalidParams("fact_not_found")
	case errors.Is(err, engine.ErrReasonRequired):
		return invalidParams("reason_required")
	default:
		return storageFailure(err)
	}
}

// jobsFailure maps a stable jobs error to a JSON-RPC error.
func jobsFailure(err error) *Error {
	switch {
	case errors.Is(err, jobs.ErrBusy):
		return failure(CodeBusy, "busy")
	case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrInvalidLease):
		return failure(CodeConflict, "conflict")
	case errors.Is(err, jobs.ErrCancelled):
		return failure(CodeConflict, "cancelled")
	case errors.Is(err, jobs.ErrNotFound):
		return invalidParams("job_not_found")
	default:
		return storageFailure(err)
	}
}

// storageFailure reports a stable storage error code without path details.
// Diagnostics remain on stderr in the host process.
func storageFailure(err error) *Error {
	_ = err
	return failure(CodeStorage, "storage_failure")
}
