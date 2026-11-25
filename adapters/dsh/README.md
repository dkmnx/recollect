# dsh-recollect

Per-workspace Recollect memory for DeepSeek Harness (DSH). The plugin gives each agent a scoped `recollect_memory` tool and injects a memory snapshot bound to that agent's working directory. Data stays on this machine.

## Requirements

- DeepSeek Harness `^0.2.3-alpha.2`.
- The `recollect` engine binary. The plugin uses `binaryPath` from its config, then `RECOLLECT_BINARY`, then the default install directory (`%LOCALAPPDATA%\Programs\recollect\recollect.exe` on Windows, `~/.local/bin/recollect` elsewhere), with PATH as a fallback.

## Install

In DSH, open **Plugins** > **Add plugin**, enter the package name `dsh-recollect@latest` (or a local directory path) in the **Package name or address** field, and select **Install**. Enable the plugin if DSH leaves it disabled after install. The tool and memory section apply at the next agent creation.

Or use the DSH CLI:

```sh
dsh plugin --profile <profile> add dsh-recollect@latest
```

Replace `<profile>` with the profile you use. The CLI rejects the Electron-managed `desktop` profile by default, so install into that profile through the GUI.

Remove it with:

```sh
dsh plugin --profile <profile> remove dsh-recollect
```

See the [changelog](CHANGELOG.md) for release history.

## Configuration

The plugin reads these keys from its config row:

| Key | Meaning |
| --- | --- |
| `binaryPath` | Optional explicit path to the `recollect` binary. |
| `userDir` | Optional root for memory files. Defaults to `%LOCALAPPDATA%\recollect` on Windows and `~/.recollect` elsewhere. |

Project memory is isolated under `<userDir>/projects/` by a hash of the canonical workspace root returned by `recollect resolve`. Different workspaces get different stores; path case, symlinks, junctions, worktrees, and trailing separators do not split one workspace into two. `userDir` must be absolute.

Each agent starts its own `recollect` engine process bound to its workspace. Agents share a store safely through the engine's locks and receipts; the cost is one process per agent. This is the accepted design for 0.1.0.

## Tool

`recollect_memory` accepts `target` (`project` or `user`) and `operation` (`read`, `search`, `append`, `correct`, `delete`), plus the fields each operation needs: `requestID` for mutations, `name` for reads, `revision` for edits, and `query` for searches. Reads return formatted fact text; other operations return JSON. `target: user` refers to one machine-wide user store shared across workspaces.

## Verify

From a checkout, run the E2E verifier with the bundled Node runtime:

```sh
node adapters/dsh/verify.mjs adapters/dsh
```

It checks the bundle manifest, patch row, locale, copied icon and license, the plugin's required services, argument validation, revision surfacing, store isolation, and a real JSON-RPC round trip through `recollect`. Live DSH activation is checked separately with `cordis_inspect` and a real `recollect_memory` call.
