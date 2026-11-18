// evolve.go — the on-demand evolution sweep behind /memory evolve: one
// model call over the fact catalog that merges near-duplicates and induces
// links through the capture op vocabulary. Unlike capture it runs on
// demand over the whole store, and it maintains what capture built: it
// never adds, moves, or re-labels a fact into existence.

package memory

// EvolvePrompt asks for a maintenance sweep over the existing fact store.
// The ops schema is the capture consolidation schema restricted to the two
// actions a sweep may take, so the reply parses and applies through the
// shared paths.
const EvolvePrompt = `You are xi's memory evolution step.
Merge near-duplicate facts and induce the links the capture pass missed, over the fact catalog below.
Return JSON only. Do not explain.

Each catalog line names one fact, its one-line description, and its current links. Work only with those names; never invent a fact, a name, or content.
Merge two or more facts that cover the same knowledge: correct the survivor (keep its name; restate its one-line description to cover the merged scope; empty content keeps its body) and drop the others.
Induce a link when a fact continues, updates, or contradicts another: correct it with links naming those facts, comma-separated, restating every link you want to keep. Omit links otherwise.
Never drop a fact you corrected in the same reply.
Most catalogs need no sweep: return {"ops":[],"reason":"..."} when nothing needs merging or linking.

Output schema:
{"ops":[{"action":"correct|drop","type":"project|user|feedback|reference|experience","key":"stable-fact-name","summary":"one line","content":"","links":"comma-separated"}],"reason":"why nothing was saved"}`

// FilterEvolveOps restricts a parsed sweep reply to the actions evolve may
// take: correct and drop. The skipped count lets the caller report what
// the reply tried outside its contract.
func FilterEvolveOps(result CaptureResult) (ops []CaptureOp, skipped int) {
	corrected := map[string]bool{}
	for _, op := range result.Ops {
		if op.Action == "correct" {
			corrected[op.Key] = true
		}
	}
	for _, op := range result.Ops {
		switch op.Action {
		case "correct":
			ops = append(ops, op)
		case "drop":
			if corrected[op.Key] {
				// A reply that drops a fact it corrected in the same pass
				// would delete the survivor it just rewrote; the pair is
				// self-contradictory, so the drop is out of contract.
				skipped++
				continue
			}
			ops = append(ops, op)
		default:
			skipped++
		}
	}
	return ops, skipped
}
