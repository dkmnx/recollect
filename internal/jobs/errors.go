package jobs

import "errors"

// Stable job failures. Callers match them with errors.Is.
var (
	ErrInvalidLease      = errors.New("invalid_lease")
	ErrCancelled         = errors.New("cancelled")
	ErrConflict          = errors.New("conflict")
	ErrNotFound          = errors.New("job_not_found")
	ErrGenerationMissing = errors.New("generation_unavailable")
	ErrBusy              = errors.New("busy")
)
