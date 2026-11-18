package jobs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"recollect/internal/memory"
)

// buildPrompt composes an engine-owned prompt. The model call itself never
// runs during ingest; the prompt is stored with the job until a host worker
// asks for it.
func (q *Queue) buildPrompt(kind Kind, window []HostRecord, facts string, payload []string) string {
	switch kind {
	case KindFact:
		return memory.CaptureConsolidationPrompt +
			"\n\nSession window (observation date " + q.now().Format(time.DateOnly) + "):\n" +
			windowText(window) + "\nFact catalog:\n" + facts
	case KindDigest:
		return memory.CaptureDigestPrompt + "\n\nSession window:\n" + windowText(window)
	case KindConsolidate:
		return memory.EvolvePrompt + "\n\nFact catalog:\n" + facts
	case KindPages:
		return memory.EvolvePagesPrompt + "\n\nRequested pages: " +
			strings.Join(payload, ", ") + "\n\nFact excerpts:\n" + facts
	}
	return ""
}

// applyOutput parses a model reply with the memory parsers and writes through
// the store. A revision job has no model reply: its payload is already the
// bounded set of chain entries to append.
func (q *Queue) applyOutput(job Job, input jobInput, output string) error {
	switch job.Kind {
	case KindFact, KindConsolidate:
		result, err := memory.ParseCaptureResult(output)
		if err != nil {
			return err
		}
		ops := result.Ops
		if job.Kind == KindConsolidate {
			ops, _ = memory.FilterEvolveOps(result)
		}
		for _, op := range ops {
			if err := memory.ApplyCaptureOp(q.store, op, namespacedSession(job), input.CapturedOn); err != nil {
				return err
			}
		}
		if job.Kind == KindConsolidate && job.Phase == "automatic" {
			return q.store.UpdateCaptureState(func(state *memory.CaptureState) bool {
				state.LastEvolveAt = q.now()
				return true
			})
		}
		return nil
	case KindDigest:
		if input.Fallback != "" {
			_, err := q.store.WriteDigest(memory.Digest{
				SessionID: namespacedSession(job),
				Topic:     "run-outcome",
				Summary:   input.Fallback,
				Date:      input.CapturedOn,
				Updated:   q.now(),
				Fallback:  true,
			})
			return err
		}
		topic, summary, err := memory.ParseCaptureDigest(output)
		if err != nil {
			return err
		}
		_, err = q.store.WriteDigest(memory.Digest{
			SessionID: namespacedSession(job),
			Topic:     topic,
			Summary:   summary,
			Date:      input.CapturedOn,
			Updated:   q.now(),
		})
		return err
	case KindRevision:
		var revisions []memory.ChainRevision
		if len(input.Payload) == 0 {
			return nil
		}
		if err := json.Unmarshal(input.Payload, &revisions); err != nil {
			return err
		}
		return q.store.AppendChainRevisions(revisions)
	case KindPages:
		result, err := memory.ParsePageDrafts(output)
		if err != nil {
			return err
		}
		var requested []string
		if err := json.Unmarshal(input.Payload, &requested); err != nil {
			return err
		}
		requestedSet := make(map[string]bool, len(requested))
		for _, name := range requested {
			requestedSet[name] = true
		}
		facts, err := q.store.ListFacts()
		if err != nil {
			return err
		}
		factNames := make(map[string]bool, len(facts))
		for _, fact := range facts {
			factNames[fact.Name] = true
		}
		pages, _ := memory.FilterPageDrafts(result, factNames, requestedSet)
		for _, page := range pages {
			if question, ok := memory.PageQuestion(page.Name); ok {
				page.Question = question
			}
			if _, err := q.store.UpsertPage(page); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("jobs: cannot apply kind %s", job.Kind)
}
