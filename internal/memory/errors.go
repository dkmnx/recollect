package memory

import (
	"errors"
	"fmt"
)

// Error codes surfaced by the memory tool and pinned by tests. Size-refusal
// codes are gone with the caps (ADR-005): the remaining failures are IO,
// invalid input, boundary rules, and the capture timeout, which is a slow
// provider rather than a file.
const (
	CodeInvalidOperation = "INVALID_OPERATION"
	CodeMissingField     = "MISSING_FIELD"
	CodeInvalidTopic     = "INVALID_TOPIC"
	CodeInvalidReply     = "INVALID_REPLY"
	CodeReasonRequired   = "REASON_REQUIRED"
	CodeOldNotFound      = "OLD_NOT_FOUND"
	CodeOldAmbiguous     = "OLD_AMBIGUOUS"
	CodeFileInvalid      = "MEMORY_FILE_INVALID"
	CodeIO               = "MEMORY_IO"
	CodeCaptureTimeout   = "CAPTURE_TIMEOUT"
	CodeDisabled         = "MEMORY_DISABLED"
	CodeDigestNotFound   = "DIGEST_NOT_FOUND"
	CodeFactNotFound     = "FACT_NOT_FOUND"
	CodeDuplicate        = "DUPLICATE_FACT"
	CodeSecretRejected   = "SECRET_REJECTED"
	CodePrivateRejected  = "PRIVATE_REJECTED"
	CodePathEscape       = "PATH_ESCAPE"
)

// Error is a memory failure with a stable code for tool results.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf builds a coded error.
func Errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// CodeOf returns the memory code for err, or CodeIO for anything else.
func CodeOf(err error) string {
	var memoryErr *Error
	if errors.As(err, &memoryErr) {
		return memoryErr.Code
	}
	return CodeIO
}
