// audit: behavior - The spawn probe checks windowsHide inside an isolated Bun test process.
// audit: regression - A visible console opens for every Recollect engine child on Windows.
// audit: coverage - The main suite cannot mock node:child_process without leaking the mock across files.
// audit: seam - The probe runs only when RECOLLECT_SPAWN_PROBE is set; no production seam is added.

import { describe, expect, mock, test } from "bun:test";

const enabled = process.env.RECOLLECT_SPAWN_PROBE === "1";

if (enabled) {
  const fakeChild = {
    stdout: { setEncoding() {}, on() {} },
    stderr: { setEncoding() {}, on() {} },
    stdin: { on() {}, writable: true, write() {} },
    on() {},
    kill() {},
  };
  mock.module("node:child_process", () => ({
    spawn(_command: string, _args: string[], options: { windowsHide?: boolean }) {
      if (options?.windowsHide !== true) {
        process.stderr.write("windowsHide missing\n");
        process.exit(2);
      }
      return fakeChild;
    },
  }));
}

const { spawnCarrier } = await import("../src/rpc.ts");

describe("spawnCarrier probe", () => {
  test.skipIf(!enabled)("passes windowsHide to child_process.spawn", () => {
    const carrier = spawnCarrier("recollect.exe");
    carrier.dispose();
  });
});
