# Recollect MCP server

`recollect mcp` serves the Model Context Protocol over stdio. It lets an MCP
client search, read, and write Recollect memory without a custom adapter.

## Install and start

```sh
go install github.com/dkmnx/recollect/cmd/recollect@latest
recollect mcp [--project DIR] [--harness NAME]
```

- `--project` binds the server to one project directory.
- `--harness` names the job owner. The default is `mcp`.
- Without `--project`, the server uses the first `file://` root from the
  client, then the current directory.

The server asks for roots after `notifications/initialized` when the client
advertises the roots capability. The binding is fixed after the first tool
call. A later `roots/list_changed` is ignored.

## Register with a client

Most clients accept a stdio server entry:

```json
{
  "mcpServers": {
    "recollect": {
      "command": "/absolute/path/to/recollect",
      "args": ["mcp", "--project", "/absolute/path/to/project"]
    }
  }
}
```

For the Codex CLI:

```sh
codex mcp add recollect -- /absolute/path/to/recollect mcp --project /absolute/path/to/project
codex mcp list
```

Use the same command and arguments in any other MCP client. Run
`recollect version` to confirm the binary is the one the client starts.

## Tools

- `memory_search`: `query` (required), `scene`, `limit`, and `target`. It is
  read only.
- `memory_read`: `name` for a project and `target`. It is read only.
- `memory_save`: `description` for a project, `name`, `body`, and `reason` for
  a user. It is not destructive and not idempotent.
- `memory_correct`: `name` for a project, `revision` (required),
  `description`, `body`, and `target`. It is destructive.
- `memory_delete`: `name` for a project, `revision` (required), and `target`.
  It is destructive.
- `memory_context`: no arguments. It is read only.

`target` is `project` or `user`. The default is `project`.

A project save needs `description`. A user save needs `reason`; `body` or
`description` supplies the line it appends to `user.md`.

`memory_correct` and `memory_delete` need the `revision` from a prior
`memory_read`. A missing revision returns a tool error that asks for the read.
A stale revision asks for a fresh read. A user correction replaces all of
`user.md`, so a blank body is rejected.

Results are JSON text in `content[0].text`. Results over 64 KiB are marked
`[truncated]`. A tool failure sets `isError: true` and returns a path-free
message.

## Resources

- `recollect://context` returns the rendered memory section.
- `recollect://status` returns JSON with the fact count and pending jobs.

## Configuration

The server reads `<userDir>/config.json`. The default user dir is
`~/.recollect` on Linux and macOS, or `%LOCALAPPDATA%\recollect` on Windows.

```json
{
  "search": { "backend": "sqlite" },
  "embedding": {
    "baseURL": "http://127.0.0.1:11434/v1",
    "model": "nomic-embed-text",
    "dimensions": 768,
    "apiKeyEnv": "EMBED_KEY"
  },
  "generation": {
    "mode": "command",
    "command": ["claude", "-p"],
    "timeoutSeconds": 120
  }
}
```

- `search.backend` is `lexical`, `sqlite`, or `hybrid`. The default is
  `lexical`.
- `hybrid` needs `embedding.baseURL`, `embedding.model`, and a positive
  `embedding.dimensions`.
- `generation.mode` is `off`, `command`, or `http`. The default is `off`.
  Deterministic jobs run even when generation is off.
- API keys come from the environment variable named by `apiKeyEnv`. The file
  never stores a key.
- `RECOLLECT_SEARCH_INDEX` overrides the search backend.
  `RECOLLECT_GENERATION=off` stops generation.

Run `recollect doctor` to print the effective config. Add `--user-dir DIR` to
check a different user root. Secrets are shown as set or unset.

## Security

- Writes pass through the same privacy filter as the engine. `<private>` spans
  are removed, and recognized credentials are replaced before they reach disk.
- Tool errors omit host paths and raw storage errors.
- The server never reads an API key from `config.json`.

## Limits

- Tool results are capped at 64 KiB.
- Roots are fixed after the first tool call.
- A user correction replaces `user.md` and rejects a blank body.
- Jobs and maintenance are not MCP tools. Use the engine protocol for those.
  See [protocol.md](protocol.md).

## Verify

1. Start the client and check that `recollect` appears in its MCP server list.
2. Call `memory_save` with a description and body.
3. Call `memory_search` for a word in the saved fact.
4. Call `memory_read` and confirm the revision and content.
5. Call `memory_correct` with that revision, then `memory_read` again.
