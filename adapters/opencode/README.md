# opencode-recollect

OpenCode plugin that connects the host to the Recollect memory engine.

The plugin starts the `recollect` binary as a child process, exposes the
`memory` tool, registers the `/recollect-*` commands, and captures session
events for durable memory.

## Requirements

- OpenCode 2.3.0 or later.
- The `recollect` engine binary, v0.1.0 or later, from a release, `go install`,
  or a source build.
- Bun only for adapter development and tests.

## Install the engine

### From a release

Download the archive for your platform from GitHub Releases and verify it
against `checksums.txt`, or run the repository install script. Replace `X.Y.Z`
with the version you want, and keep the `v` in commands that take a tag.

```sh
tar -xzf recollect_X.Y.Z_linux_amd64.tar.gz
install -m 755 recollect ~/.local/bin/recollect
recollect version
```

The one-line form is:

```sh
curl -fsSL https://raw.githubusercontent.com/dkmnx/recollect/main/scripts/install.sh | sh
```

The download-and-inspect form is:

```sh
curl -fsSL https://raw.githubusercontent.com/dkmnx/recollect/main/scripts/install.sh -o install.sh
less install.sh
sh install.sh --version vX.Y.Z
```

### With go install

```sh
go install github.com/dkmnx/recollect/cmd/recollect@latest
recollect version
```

### From source

```sh
git clone https://github.com/dkmnx/recollect
cd recollect
go build -o recollect ./cmd/recollect
./recollect version
```

Put the binary on `PATH`, or leave the default install location. The plugin
finds it automatically; an explicit path is only needed for a custom install.

## Add the plugin to OpenCode

### Published package

Use the published npm package:

```jsonc
{
  "$schema": "https://opencode.ai/config.json",
  "plugins": [
    {
      "package": "opencode-recollect@0.1.1",
      "options": {
        "binaryPath": "/home/you/.local/bin/recollect",
        "userDir": "/home/you/.recollect",
        "generationPolicy": "host"
      }
    }
  ]
}
```

Pin the version. `opencode-recollect@latest` also works, but a later release
can change under you. The `binaryPath` option is optional when the engine is
on `PATH` or in the default install directory.

OpenCode installs a missing package in the background at server startup, so
you do not run `npm install`. The CLI can add the package to the global config
and install it in one step:

```sh
opencode plugin add opencode-recollect@0.1.1
opencode plugin check
```

Exact versions stay pinned. `opencode plugin update` moves an unpinned package,
and `opencode plugin remove opencode-recollect@0.1.1` removes it.

### From a checkout

The engine prints the exact local entry for this machine:

```sh
recollect setup opencode
```

If the adapter is not beside the binary or in the current repository, pass its
directory:

```sh
recollect setup opencode --adapter /path/to/recollect/adapters/opencode
```

Pass `--write` to merge the entry into the global `opencode.json`. The command
does not rewrite `opencode.jsonc`, because a JSON encoder would drop its
comments. Run `recollect doctor` to probe the engine before you restart the
host.

To add the local entry by hand, use the `plugins` array in your OpenCode
config. Use the global config at `~/.config/opencode/opencode.jsonc` or a
project config at `./opencode.jsonc`. The `file://` package form is for
development from a checkout.

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

### Binary detection

When `binaryPath` is omitted, the adapter checks in this order:

1. The `binaryPath` option.
2. The `RECOLLECT_BINARY` environment variable.
3. Each `PATH` directory, including `PATHEXT` entries on Windows.
4. `~/.local/bin/recollect` or
   `%LOCALAPPDATA%\Programs\recollect\recollect.exe`.

If none exist, the plugin fails with an error that names all four checks.

### Options

| Option                  | Required | Meaning                                                                                        |
| ----------------------- | -------- | ---------------------------------------------------------------------------------------------- |
| `binaryPath`            | No       | Absolute path to the `recollect` binary. Omit it to use the detection order above.             |
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

## Test

Run the adapter checks from this directory.

```sh
bun install --frozen-lockfile
bun run typecheck
bun test
```

## Troubleshooting

- **Plugin does not load.** Check that `package` points at the adapter
  directory that contains `package.json`, and that the path is absolute.
- **Setup cannot find the adapter.** Pass
  `--adapter /path/to/adapters/opencode`; a binary installed with
  `go install` sits outside the repository.
- **Engine fails to start.** Run `binaryPath` directly. A missing execute bit,
  a wrong architecture, or a stale build fails the same way in OpenCode.
- **Wrong version.** Run `recollect version` and reinstall or rebuild the
  binary the config names.
- **No memory tool.** Confirm the plugin entry is under `plugins`, not
  `plugin`, and restart OpenCode after editing the config.
- **Capture is disabled.** Set `generationPolicy` to `host` and confirm the
  host exposes `experimental.generate.text`.

## Changelog

See [CHANGELOG.md](CHANGELOG.md).
