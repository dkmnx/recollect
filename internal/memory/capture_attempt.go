// capture_attempt.go — the persisted record of the most recent capture
// phases. Capture trouble used to reach the user as one transient notice
// and then vanish: nothing survived a restart, so a reply the model cut
// mid-sentence looked identical to a reply that never arrived. The record
// keeps the transport facts (how the stream ended, how many bytes came
// back, how many calls the phase spent) beside the failure the parser
// reported, one slot per phase so a rejected consolidation is not masked
// by the digest that ran after it.

package memory

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// CapturePhase names the pass phase a record describes.
type CapturePhase string

const (
	CapturePhaseTyped  CapturePhase = "typed"
	CapturePhaseDigest CapturePhase = "digest"
)

// CaptureAttempt is what one phase saw. StopReason and Truncated separate
// a cap cut from a body that ended early under a clean terminal event:
// the first earns the doubled-cap retry, the second earns the parse
// retry, and a report that cannot tell them apart cannot choose.
type CaptureAttempt struct {
	Phase CapturePhase `json:"phase"`
	At    time.Time    `json:"at"`
	// Duration is the wall time the phase spent, including its retries.
	Duration time.Duration `json:"duration"`
	// Attempts counts the calls this phase's own loop made. A token-cap
	// retry inside one reply call is not itemized: the record reports the
	// attempt that answered, not the transport ladder beneath it.
	Attempts int `json:"attempts"`
	// Bytes is the reply length the last attempt returned, partial or not.
	Bytes      int    `json:"bytes"`
	StopReason string `json:"stopReason,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
	// Model is the model that answered the pass. It differs from the
	// configured capture model when the provider refused that one and the
	// pass ran on the session model.
	Model string `json:"model,omitempty"`
	// RefusedModel is the capture model the provider refused, empty when
	// the pass ran on the configured model.
	RefusedModel string `json:"refusedModel,omitempty"`
	// Err is the phase failure: a provider error, a spent budget, or the
	// parse diagnosis for a body the parsers rejected.
	Err string `json:"err,omitempty"`
}

// CaptureAttempts holds the last record for each phase of a capture pass.
// The zero value reads as "no pass has run".
type CaptureAttempts struct {
	Typed  CaptureAttempt `json:"typed"`
	Digest CaptureAttempt `json:"digest"`
}

// Phase returns one phase's record.
func (a CaptureAttempts) Phase(phase CapturePhase) CaptureAttempt {
	if phase == CapturePhaseDigest {
		return a.Digest
	}
	return a.Typed
}

// Set replaces one phase's record, leaving the other phase's slot alone.
func (a *CaptureAttempts) Set(phase CapturePhase, attempt CaptureAttempt) {
	if phase == CapturePhaseDigest {
		a.Digest = attempt
		return
	}
	a.Typed = attempt
}

// ReadCaptureAttempts returns the recorded phases; a missing file is the
// zero record.
func (s *Store) ReadCaptureAttempts() (CaptureAttempts, error) {
	data, err := os.ReadFile(CaptureAttemptFile(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return CaptureAttempts{}, nil
	}
	if err != nil {
		return CaptureAttempts{}, Errorf(CodeIO, "read capture attempts: %v", err)
	}
	var attempts CaptureAttempts
	if err := json.Unmarshal(data, &attempts); err != nil {
		return CaptureAttempts{}, Errorf(CodeIO, "parse capture attempts: %v", err)
	}
	return attempts, nil
}

// WriteCaptureAttempt persists one phase's record atomically, keeping the
// other phase's record.
func (s *Store) WriteCaptureAttempt(phase CapturePhase, attempt CaptureAttempt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempts, err := s.readCaptureAttemptsLocked()
	if err != nil {
		return err
	}
	attempts.Set(phase, attempt)
	data, err := json.Marshal(attempts)
	if err != nil {
		return Errorf(CodeIO, "encode capture attempts: %v", err)
	}
	return s.writeLocked(CaptureAttemptFile(s.projectDir), string(data))
}

// readCaptureAttemptsLocked reads the record file for a read-modify-write;
// the caller holds s.mu.
func (s *Store) readCaptureAttemptsLocked() (CaptureAttempts, error) {
	data, err := os.ReadFile(CaptureAttemptFile(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return CaptureAttempts{}, nil
	}
	if err != nil {
		return CaptureAttempts{}, Errorf(CodeIO, "read capture attempts: %v", err)
	}
	var attempts CaptureAttempts
	if err := json.Unmarshal(data, &attempts); err != nil {
		return CaptureAttempts{}, Errorf(CodeIO, "parse capture attempts: %v", err)
	}
	return attempts, nil
}
