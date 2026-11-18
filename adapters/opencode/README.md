# opencode-recollect

OpenCode plugin that connects the host to the Recollect memory engine.

## Install

Install the plugin in the OpenCode plugin directory. The host supplies
`effect` and `@opencode/plugin` at runtime. The plugin adds no other package.

## Configure

Set the plugin options:

- `binaryPath` (required): absolute path to the `recollect` engine binary.
- `userDir` (optional): absolute path to the shared user memory root.

The plugin never downloads a binary. It starts the configured binary as a
child process and stops it when the plugin scope closes.

Default user roots:

- Linux and macOS: `~/.recollect`
- Windows: `%LOCALAPPDATA%\recollect`

## Commands

- `/recollect-status` reports engine health and pending work.
- `/recollect-refresh` replaces the session memory snapshot.

Mode switching and manual maintenance are deferred until the engine exposes
those actions through `maintenance.execute`.

## Test

Run `bun test` from this directory.
