// audit: behavior - spawnCarrier passes windowsHide to child_process.spawn.
// audit: regression - A visible console opens for every Recollect engine child on Windows.
// audit: coverage - The rpc tests use fake carriers plus one real short-lived process, but never inspect spawn options.
// audit: seam - A guarded Bun test file runs the module mock in a separate process.

import { describe, expect, test } from "bun:test";

const probe = `${import.meta.dir}/../test/spawn_options_probe.test.ts`;

describe("spawnCarrier", () => {
  test("passes windowsHide so the engine does not open a console", async () => {
    const child = Bun.spawn([process.execPath, "test", probe], {
      cwd: `${import.meta.dir}/..`,
      env: { ...process.env, RECOLLECT_SPAWN_PROBE: "1" },
      stdout: "pipe",
      stderr: "pipe",
    });
    const stderr = await new Response(child.stderr).text();
    expect(await child.exited, stderr).toBe(0);
  });
});
