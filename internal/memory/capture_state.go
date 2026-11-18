// capture_state.go — the persisted capture markers: how far the transcript
// was consolidated and when the last digest and typed pass ran, so a
// restart does not re-capture the same window.

package memory

import (
	"encoding/json"
	"errors"
	"os"
	"time"
)

// CaptureState tracks the last capture for one project. SessionID is the
// session the message marker indexes: a new or resumed session resets the
// marker, so its opening is captured instead of skipped.
type CaptureState struct {
	LastConsolidatedMessage int `json:"lastConsolidatedMessage"`
	// LastDigestMessage is the digest phase's own cursor. It trails
	// LastConsolidatedMessage exactly when a digest failed or has not run
	// yet, so the window the digest still owes survives a consolidation
	// that landed in the same pass. A store written before this field
	// reads as zero, so the next due digest covers the bounded window once
	// and the cursor catches up.
	LastDigestMessage int `json:"lastDigestMessage"`
	// LastChainMessage is the revision chain's cursor: how far the
	// transcript's edit calls were scraped into chains.jsonl. LastChainSession
	// binds it to that transcript: a new session's transcript is a fresh
	// history scraped from zero, and a transcript that shrank under its
	// marker (compaction) re-scrapes from zero like the consolidation and
	// digest markers, so the retained tail bounds the repeat. A store
	// written before either field existed catches up over its current
	// window once.
	LastChainMessage int       `json:"lastChainMessage"`
	LastChainSession string    `json:"lastChainSession,omitempty"`
	SessionID        string    `json:"sessionID,omitempty"`
	LastDigestAt     time.Time `json:"lastDigestAt"`
	LastTypedAt      time.Time `json:"lastTypedAt"`
	// LastEvolveAt ages the automatic evolve sweep (ultra mode): a store
	// written before this field reads as never-swept, so the first eligible
	// trigger sweeps once and the stamp catches up.
	LastEvolveAt time.Time `json:"lastEvolveAt,omitempty"`
	// ChainBackfill records which files' git history the revision chain
	// already scraped: "" pending, a commit sha done, "-" no history. A
	// store written before the field reads as empty.
	ChainBackfill map[string]string `json:"chainBackfill,omitempty"`
	// TypedFailures counts consecutive typed passes whose ops all failed;
	// the parking signal for a store that cannot land anything.
	TypedFailures int `json:"typedFailures"`
}

// ReadCaptureState returns the stored markers; a missing file is zero state.
func (s *Store) ReadCaptureState() (CaptureState, error) {
	data, err := os.ReadFile(CaptureStateFile(s.projectDir))
	if errors.Is(err, os.ErrNotExist) {
		return CaptureState{}, nil
	}
	if err != nil {
		return CaptureState{}, Errorf(CodeIO, "read capture state: %v", err)
	}
	var state CaptureState
	if err := json.Unmarshal(data, &state); err != nil {
		return CaptureState{}, Errorf(CodeIO, "parse capture state: %v", err)
	}
	return state, nil
}

// UpdateCaptureState applies mutate to the persisted markers under the
// store mutex, so a read-modify-write issued from outside the capture pass
// (the evolve sweep's stamp) cannot revert a concurrent pass's marker
// advance: the mutex spans the read, the mutation, and the write. Mutate
// reports whether anything changed; an unchanged state skips the write.
func (s *Store) UpdateCaptureState(mutate func(*CaptureState) bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.ReadCaptureState()
	if err != nil {
		return err
	}
	if !mutate(&state) {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return Errorf(CodeIO, "encode capture state: %v", err)
	}
	return s.writeLocked(CaptureStateFile(s.projectDir), string(data))
}

// WriteCaptureState persists the markers atomically.
func (s *Store) WriteCaptureState(state CaptureState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return Errorf(CodeIO, "encode capture state: %v", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(CaptureStateFile(s.projectDir), string(data))
}
