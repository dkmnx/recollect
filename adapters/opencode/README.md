# opencode-recollect

OpenCode plugin that connects the host to the Recollect memory engine.

The plugin starts the `recollect` binary as a child process, exposes the
`memory` tool, registers the `/recollect-*` commands, and captures session
events for durable memory.

## Requirements

- OpenCode 2.3.0 or later.
- The `recollect` engine binary, built from this repository.
- Bun only for adapter development and tests.

## Build the engine

Build the binary from the repository root. Put it on your `PATH` or use an
absolute path in the plugin options.

```sh
git clone https://github.com/dkmnx/recollect
cd recollect
go build -o recollect ./cmd/recollect
```

Confirm it runs:

```sh
./recollect --help
```

## Add the plugin to OpenCode

The engine prints the exact entry for this machine:

```sh
./recollect setup opencode
```

Pass `--write` to merge it into the global `opencode.json`. The command does
not rewrite `opencode.jsonc`, because a JSON encoder would drop its comments.
Run `./recollect doctor` to probe the engine before you restart the host.

To add the entry by hand, use the `plugins` array in your OpenCode config. Use
the global config at `~/.config/opencode/opencode.jsonc` or a project config at
`./opencode.jsonc`.

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "plugins": [
    {
      "package": "file:///home/you/Projects/recollect/adapters/opencode",
      "options": {
        "binaryPath": "/home/you/Projects/recollect/recollect",
        "userDir": "/home/you/.recollect",
        "generationPolicy": "host"
      }
    }
  ]
}
```

Replace the paths with absolute paths on your machine. Configure the plugin
**directory**, not `src/index.ts`; OpenCode reads the package entry point from
`package.json`.

On Windows, use forward slashes or escaped backslashes in JSON:

```jsonc
{
  "package": "file:///C:/Users/you/Projects/recollect/adapters/opencode",
  "options": {
    "binaryPath": "C:/Users/you/Projects/recollect/recollect.exe"
  }
}
```

### Options

| Option                  | Required | Meaning                                                                                        |
| ----------------------- | -------- | ---------------------------------------------------------------------------------------------- |
| `binaryPath`            | Yes      | Absolute path to the `recollect` binary.                                                       |
| `userDir`               | No       | Shared user memory root. Defaults to `~/.recollect` or `%LOCALAPPDATA%\recollect`.             |
| `generationPolicy`      | No       | `host` lets the adapter call the session model for capture. `off` disables it. Default `host`. |
| `generationTimeoutMs`   | No       | Per-call generation budget from 1 through 120000. Default 120000.                              |

The plugin never downloads a binary. It starts the configured binary and stops
it when the plugin scope closes.

## Verify

Restart OpenCode, then run:

```text
/recollect-status
```

The status reports engine health and pending work. Then ask the agent to
remember a fact and read it back through the `memory` tool. Use
`/recollect-refresh` to replace the session memory snapshot after an external
change.

## Commands

- `/recollect-status` reports engine health and pending work.
- `/recollect-refresh` replaces the session memory snapshot.

## Troubleshooting

- **Plugin does not load.** Check that `package` points at the adapter
  directory that contains `package.json`, and that the path is absolute.
- **Engine fails to start.** Run `binaryPath` directly. A missing execute bit,
  a wrong architecture, or a stale build fails the same way in OpenCode.
- **No memory tool.** Confirm the plugin entry is under `plugins`, not
  `plugin`, and restart OpenCode after editing the config.
- **Capture is disabled.** Set `generationPolicy` to `host` and confirm the
  host exposes `experimental.generate.text`.

## Test

Run the adapter checks from this directory.

```sh
bun install --frozen-lockfile
bun run typecheck
bun test
```
