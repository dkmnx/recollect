// capture_apply.go — applying consolidation ops to the fact store. demote
// moves a fact file into the overflow topic directory, drop removes a fact,
// and correct rewrites a fact in place. No op is refused for size; the only
// failures are IO and invalid input, so a retried pass can always converge.

package memory

import "time"

// CaptureOverflowTopic receives detail moved out of the root by a demote
// op. One fixed topic keeps the demotion path bounded: the write either
// lands there or fails, and the topic list shows it.
const CaptureOverflowTopic = "capture-overflow"

// ApplyCaptureOp applies one consolidation op to the store. A missing fact
// is already the end state a drop asks for, so it is a no-op, not a
// failure. originSession stamps the fact's provenance when the capture
// knows the session id. capturedOn is the window's observation date: new
// facts record when the conversation happened, not when the capture ran;
// zero falls back to the store clock.
func ApplyCaptureOp(s *Store, op CaptureOp, originSession string, capturedOn time.Time) error {
	op.Type = NormalizeFactType(op.Type)
	switch op.Action {
	case "correct":
		// A nil Links keeps the fact's links; the op carries links only
		// when the model set them.
		_, err := s.UpdateFact(op.Key, op.Summary, op.Content, op.Links)
		return err
	case "demote":
		_, err := s.MoveFactToTopic(op.Key, CaptureOverflowTopic)
		return err
	case "drop":
		_, err := s.DeleteFact(op.Key)
		return err
	}
	fact := Fact{
		Name:            op.Key,
		Description:     op.Summary,
		Type:            op.Type,
		Scene:           op.Scene,
		Body:            op.Content,
		OriginSessionID: originSession,
		Links:           op.Links,
	}
	if op.Scene == "" {
		// An update without a scene keeps the fact's scene: UpsertFact
		// replaces the whole record, and absence must not wipe a tag the
		// consolidation model never saw. shortcut: a non-NotFound read
		// error also leaves the fact unscoped here, but the same IO failure
		// fails the UpsertFact write below, so the op errors and the
		// capture pass retries.
		if existing, err := s.ReadFact(op.Key); err == nil {
			fact.Scene = existing.Scene
		}
	}
	if !capturedOn.IsZero() {
		fact.CapturedOn = capturedOn.Format(time.DateOnly)
	}
	_, err := s.UpsertFact(fact)
	return err
}
