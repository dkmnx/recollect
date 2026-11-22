# Recollect stdio protocol

Reference for the newline-delimited JSON-RPC 2.0 protocol. `recollect` serves it
on stdin and stdout when you run it with no command.

## Framing

- One JSON object per line, encoded as UTF-8, in each direction.
- Blank and whitespace-only lines are ignored.
- A frame larger than 16 MiB (16,777,216 bytes) stops the stream with a scanner
  error.
- A request needs `"jsonrpc": "2.0"` and a non-empty `method`.
- A reply carries the request `id`. A frame that is not valid JSON replies with
  `id: null`.
- Diagnostics go to stderr. stdout carries replies only.
- Requests run in order.

## Versioning

- `initialize` negotiates `protocol.major`. Major 1 is the only supported major.
- A non-zero minor is accepted and reported in the `note` field.
- A minor version only adds optional fields or methods. A major version may
  change or remove fields and must be negotiated at `initialize`.

## Errors

- `-32600`: invalid request. A malformed frame, initialize required, already
  initialized, or engine closed.
- `-32601`: unknown method.
- `-32602`: invalid params, including `fact_not_found` and `reason_required`.
- `-32001`: unsupported protocol major.
- `-32002`: read required before a mutation.
- `-32003`: revision conflict.
- `-32004`: busy.
- `-32005`: conflict.
- `-32006`: storage failure.

Mapped domain errors use stable messages such as `fact_not_found`,
`read_required`, `revision_conflict`, and `storage_failure`. The protocol does
not expose host paths or raw error text. `status.get` reports the latest error
message as `lastErrorCode`; it is a string, not a number.

## Methods

### initialize

Binds the process to one project and one user root. Call it once.

- `projectDir` (string, required): per-project memory directory.
- `projectRoot` (string): optional canonical root; defaults to `projectDir`.
- `userDir` (string, required): shared user memory root.
- `harnessID` (string): host identifier, for example `opencode`.
- `capabilities` (string[]): supported host capabilities.
- `generationState` (string): `enabled`, `disabled`, or `unavailable`. The
  default is `unknown`.
- `protocol` (object): `{ "major": 1, "minor": 0 }`. Default major 1.

Result: `jsonrpcVersion`, `protocol`, `projectDir`, `userDir`, `harnessID`,
`capabilities`, `generationState`, and an optional `note`.

The `generation` capability is kept only when `generationState` is `enabled`.
A second `initialize` fails with `-32600` and `already initialized`.

### memory.execute

Common params: `target` is `project` or `user`; `operation` is `read`, `search`,
`append`, `correct`, or `delete`.

Every result includes `target` and `operation`.

- `read` needs `name` for the project target. It returns `name`, `revision`,
  `content`, and `fact`. `fact` is present only for a project read.
- `search` needs `query`. It returns `matches` for the project target and
  `lineMatches` for the user target.
- `append` needs `requestID`. A project append needs `description`; `name` and
  `body` are optional. A user append needs `reason`; `body` or `description`
  supplies the line. It returns `name`, `requestID`, and `revision`.
- `correct` needs `requestID` and `revision`. A project correction needs `name`
  and takes `description` or `body`. A user correction replaces `user.md` with
  `body`. It returns `name`, `requestID`, and `revision`.
- `delete` needs `requestID` and `revision`. A project deletion needs `name`.
  It returns `name` and `requestID`.

`search` accepts `scene` and `limit` for the project target. Project matches
carry `name`, `description`, `body`, `field`, and `kind`. A `field` value names
the credited field. A `kind` of `semantic` marks a vector-only match. User
search returns `lineMatches` with line numbers and context.

`append`, `correct`, and `delete` are idempotent by `requestID`. A replay
returns the recorded result for 30 days. A request ID is hashed before it
becomes a state file name.

#### Read before mutate

`correct` and `delete` require the `revision` from a prior `read` of the same
target. A missing revision returns `-32002` with `read_required`. A revision
that is no longer current returns `-32003` with `revision_conflict`. Neither
case changes stored content.

A matching `requestID` receipt replays before the revision check, so a retry
returns the recorded result even if the record changed since. A new mutation
checks the current revision.

#### Target-specific writes

- A project `append` needs `description`. `name` defaults from the description,
  and `body` is optional. A missing description fails with `-32006` and
  `storage_failure`.
- A user `append` needs `reason`. It appends one line to `user.md`. `body` or
  `description` supplies the line, and `name` is ignored.
- A project `correct` updates `description` and `body`. An omitted field keeps
  its stored value.
- A user `correct` replaces all of `user.md` with `body`. An omitted body
  clears the file.

### context.get

`refresh` rebuilds the rendered section instead of returning the cached one.

Result: `snapshotID` and the rendered `section`. The section is filtered for
private content and secrets before it leaves the process.

### events.ingest

`records` is an array of host records. Each has `id`, `sessionID`,
`generation`, `time`, `role`, `text`, optional `synthetic`, and optional
`toolCalls`.

`boundaries` is an array of run boundaries. Each has `sessionID`, `generation`,
`kind`, `time`, and `available`.

A tool call has `id`, `name`, `class`, `outcome`, and optional `arguments` and
`editEvidence`.

Result: `accepted`, `dropped`, and `pending`.

### jobs.next

| Param        | Type | Notes                                                 |
| ------------ | ---- | ----------------------------------------------------- |
| `generation` | bool | Lease only jobs this host can generate. Default true. |

Result: `job` and `lease`. `job` is null when no job is available. A job has
`id`, `kind`, `harnessID`, `state`, optional `prompt`, and `deterministic`.
Deterministic jobs need no model call.

### jobs.complete

`jobID` and `lease` are required. `lease` is the value from `jobs.next`.
`output` carries the model text. `failure` is one of `cancelled`,
`generation_unavailable`, `generation_timeout`, or `generation_failed`.

Result: `id` and `applied`.

### status.get

Result: `health`, `pending`, `conflicts`, `generationState`, `jobStates`,
`privacyBlocked`, `facts`, `userBytes`, `digests`, `pagesNeedRefresh`, `limits`,
and an optional `lastErrorCode`. `limits` reports `projectSoftBytes`,
`userInjectBytes`, `pagesInjectBytes`, `maxSearchResults`, `maxDigests`, and
`digestTTLDays`.

### maintenance.execute

- `status` returns `action` and `pending`.
- `refresh` returns `action`.
- `import` takes `sourceProjectDir`, `sourceUserDir`, and optional `dryRun`. It
  returns an import report.
- `import-status` returns `report` and `exists`.

### engine.close

| Param        | Type   | Notes         |
| ------------ | ------ | ------------- |
| `deadlineMS` | number | Default 5000. |

Result: `count`, `deadlineMS`, and `pending` job views. After a successful
close, every method except `initialize` returns `-32600` with `engine closed`.
