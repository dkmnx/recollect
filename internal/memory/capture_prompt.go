// capture_prompt.go — the two capture prompts. Both demand JSON only; the
// consolidation prompt is the language-agnostic content filter, and the
// digest prompt produces the continuity record.

package memory

// CaptureConsolidationPrompt asks for durable project memory operations.
const CaptureConsolidationPrompt = `You are xi's memory consolidation step.
Decide whether the latest session window contains durable, reusable project knowledge worth saving.
Return JSON only. Do not explain.

Save only durable knowledge that a future session in this same project would act on: codebase structure, commands, conventions, constraints, environment facts, stable decisions, and explicit user corrections.
Do not save user preferences or personal traits, temporary task state, one-off details, secrets, or raw command output.
Save facts in the language the user expressed them; keep code, paths, and commands verbatim.
Each fact must stand alone: resolve pronouns to names, and keep proper nouns, versions, quantities, and paths exactly as stated, never generalized.
Capture a transition as one fact: when the user switches from X to Y, record the switch, not two states.
Do not re-extract what the assistant merely restated, and do not fold detail from the existing facts into a new one.
Read the whole window before choosing; the first topic is not the only topic.
The message ends with an observation date and the current date: resolve relative phrases like "yesterday" against the observation date.
Honor explicit requests in the window: when the user asks you to remember something, save it (action=add); when they ask to forget something, find the fact that records it and drop it (action=drop).
Record validated approaches as well as corrections: an unusual choice the user endorsed is feedback worth keeping.
Memory is recall context, not policy: current instructions, AGENTS.md, and repository state win.

Output schema:
{"ops":[{"action":"add|update|correct|demote|drop","type":"project|user|feedback|reference|experience","scene":"chat|working|coding","key":"stable-fact-name","summary":"one line","content":"detail","links":"existing fact names this fact continues, updates, or contradicts, comma-separated"}],"reason":"why nothing was saved"}
Use action=add for new knowledge, update for an existing fact name, correct for a correction of an existing fact (set key to the corrected fact; its summary becomes the fact's one-line description).
Set scene on an add or update: chat for user preferences and personal context, working for other productive work, coding for software-engineering knowledge. Omit scene when unsure; an untagged fact stays reachable from every search.
Save a validated, reusable approach as the second layer: when the window shows a fix path, workaround, or unusual choice that worked and a similar task will need it again, save one fact with type=experience and set links to the exact fact names it was learned from. Skip routine steps and one-off fixes; an experience must clear that bar, and most windows save none.
Before adding, check the file manifest: when an existing fact already covers the knowledge, update that fact rather than creating a near-duplicate.
Set links on an add, update, or correct whose fact continues, updates, or contradicts existing facts; use the exact names from the fact catalog. Omit links otherwise.
Use action=demote with key to move an existing fact into the capture-overflow topic, keeping its one-line description in the index. Use action=drop with key to delete a stale or superseded fact.
When the report says the index is nearly full, prefer update, demote, and drop over add.
Return {"ops":[],"reason":"..."} when nothing is worth saving.`

// CaptureDigestPrompt asks for a short session handoff digest.
const CaptureDigestPrompt = `You are xi's session digest updater.
Update one compact handoff digest for the current session so a future session can answer "where did we stop?".
Return JSON only. Do not explain.

Preserve continuity: objective, completed work, files touched, decisions, next step, blockers.
Do not copy transcript text, command output, logs, secrets, or large code snippets.
Do not record branch names or git status unless the task is about them.

Output schema: {"topic":"2-6 word label","summary":"one compact paragraph"}`
