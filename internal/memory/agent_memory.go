// agent_memory.go — per-subagent persistent memory: the directory layout
// under <agentDir>/memory/agent-memory/, the key rule, and the prompt
// section a memory-enabled child spawns with. User scope only (ADR-006):
// everything lives under the agent dir, never the workspace. No capture
// runner touches these directories (ADR-008) — they are model-driven only.

package memory

import (
	"path/filepath"
	"strings"
)

// agentMemoriesDirName holds one directory per memory-enabled agent profile.
const agentMemoriesDirName = "agent-memory"

// AgentMemoriesRoot is the parent of every agent memory directory.
func AgentMemoriesRoot(agentDir string) string {
	return filepath.Join(MemoryRoot(agentDir), agentMemoriesDirName)
}

// AgentMemoryRoot is the memory directory for one agent key.
func AgentMemoryRoot(agentDir, key string) string {
	return filepath.Join(AgentMemoriesRoot(agentDir), key)
}

// SanitizeAgentMemoryKey maps a profile name onto a filesystem-safe
// directory key: every rune outside [a-zA-Z0-9_-] becomes a dash, and an
// empty result falls back to "unknown". The rule matches ZCode's agent-key
// sanitization so profile names stay convention-compatible.
func SanitizeAgentMemoryKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}

// MemoryInstructions is the shared memory contract injected with both the
// workspace project prompt and the agent prompt, so the two cannot drift.
const MemoryInstructions = `Memory is a hint, not live state. Verify before acting on it; entries can be stale.

Write only when a future session would plausibly act better because of it. No-op is the
preferred outcome. Never save one-off requests, status updates, temporary facts, secrets,
credentials, or speculation. Code patterns, git history, and fix recipes are derivable from
the repo; do not duplicate them here. When the user explicitly asks you to remember
something, save it immediately as the type that fits; when they ask to forget something,
find and delete the fact that records it. These exclusions hold even for explicit requests:
if asked to save an activity list, ask what was surprising or non-obvious and keep that.

Choose the narrowest layer:
1. Shareable with the team and true for this repository? Put it in AGENTS.md or a referenced file, not memory.
2. True only for this project, personal or local? Project memory (target=project).
3. Would change for a different user? User memory (target=user); append requires a reason.
4. Long procedures and detail belong in topic directories; keep MEMORY.md short.

Fact types (metadata.type):
- project: ongoing work, decisions, constraints, and environment facts not derivable from
  code or git history. Convert relative dates to absolute. Lead with the fact, then a Why
  line (the motivation) and a How-to-apply line (when it should shape behavior); project
  facts decay fast, and the why keeps future reads able to judge them.
- feedback: guidance the user gave about how to work — corrections AND validated approaches
  (when they endorse an unusual choice, record that too, or you will drift cautious). Lead
  with the rule, then Why and How-to-apply, so edge cases stay judgeable.
- user: who the user is — role, expertise, preferences — as it should shape this project's work.
- reference: where things live outside the repo (trackers, dashboards, tickets, boards).

Before saving, search for an existing fact that already covers it and update that one instead
of adding a near-duplicate; keep name, description, and type current with the body. "The
memory says X exists" is not "X exists now": a fact naming a file or symbol is a claim from
its write time — verify against the repo before recommending it. The injected index is a
catalog, not the store: when it hints at an answer, read the exact fact before answering.
Search before appending and append only new entries; correct a fact with the correct
operation; use edit only to reword an entry. Write in the user's language; keep code
identifiers, paths, and commands verbatim.`

// AgentMemoryScopeLine steers a memory-enabled child at general learnings
// and states the addressing its agent-scoped tool provides: its project
// layer is its own cross-project directory and repo-true findings stay out
// of user-scope memory (ADR-006).
const AgentMemoryScopeLine = "Scope: this is user-level memory — it applies across all projects; " +
	"keep learnings general rather than project-specific. " +
	"target=project addresses your own agent memory directory; target=user is the user's global profile. " +
	"Repo-true, team-shareable findings belong in your report to the parent, not memory."

// agentMemoryEmptyIndexLine is the index body when the store read fine and
// holds nothing yet.
const agentMemoryEmptyIndexLine = "Your agent memory index (MEMORY.md) is currently empty; facts you save appear there."

// AgentMemoryPromptSection builds the system-prompt section a memory-enabled
// subagent spawns with: the shared contract, the user-scope steering, then
// the agent's own index truncated at the injection caps. A read error omits
// the index body entirely rather than claiming the memory is empty (the
// child would re-derive and re-append facts that already exist); an empty
// store still teaches — the child needs the contract to make its first save.
func AgentMemoryPromptSection(indexContent string, readErr error) string {
	section := "\n\n## Memory\n\n" + MemoryInstructions + "\n\n" + AgentMemoryScopeLine
	if readErr != nil {
		return section
	}
	if body := TruncateIndex(indexContent); body != "" {
		return section + "\n\n" + body
	}
	return section + "\n\n" + agentMemoryEmptyIndexLine
}
