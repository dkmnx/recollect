// audit: behavior - the adapter resolves the engine from explicit config, env, PATH, or the default install directory.
// audit: regression - an omitted binaryPath failed even when recollect was installed on PATH.
// audit: coverage gap - existing config tests always passed an explicit binaryPath.
// audit: regression - A Windows drive letter was split as a Linux PATH separator.
// audit: seam - resolveConfig with an injected host platform and environment that match the temp path.

import { describe, expect, test } from "bun:test";
import { chmodSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ConfigError, resolveConfig } from "./config";

const location = { project: { canonical: "/work/demo" } };

function makeExecutable(dir: string, name = "recollect"): string {
  mkdirSync(dir, { recursive: true });
  const path = join(dir, name);
  writeFileSync(path, "#!/bin/sh\nexit 0\n");
  chmodSync(path, 0o755);
  return path;
}

describe("binary detection", () => {
  test("uses an explicit binaryPath before anything else", () => {
    const config = resolveConfig(
      { binaryPath: "/opt/recollect" },
      location,
      { platform: "linux", env: { HOME: "/home/u", PATH: "/usr/bin" } },
    );
    expect(config.binaryPath).toBe("/opt/recollect");
  });

  test("uses RECOLLECT_BINARY when binaryPath is omitted", () => {
    const dir = mkdtempSync(join(tmpdir(), "rc-env-"));
    const binary = makeExecutable(dir);
    const config = resolveConfig(
      {},
      location,
      { platform: "linux", env: { HOME: "/home/u", PATH: "", RECOLLECT_BINARY: binary } },
    );
    expect(config.binaryPath).toBe(binary);
  });

  test("finds recollect on PATH", () => {
    const dir = mkdtempSync(join(tmpdir(), "rc-path-"));
    const windows = process.platform === "win32";
    const binaryName = windows ? "recollect.exe" : "recollect";
    const binary = makeExecutable(dir, binaryName);
    const config = resolveConfig(
      {},
      location,
      windows
        ? { platform: "win32", env: { PATH: dir, PATHEXT: ".exe" } }
        : { platform: "linux", env: { HOME: "/home/u", PATH: dir } },
    );
    expect(config.binaryPath).toBe(binary);
  });

  test("falls back to the default install directory", () => {
    const home = mkdtempSync(join(tmpdir(), "rc-home-"));
    const binary = makeExecutable(join(home, ".local", "bin"));
    const config = resolveConfig(
      {},
      location,
      { platform: "linux", env: { HOME: home, PATH: "" } },
    );
    expect(config.binaryPath).toBe(binary);
  });

  test("finds the Windows default install path", () => {
    const local = mkdtempSync(join(tmpdir(), "rc-win-"));
    const binary = makeExecutable(join(local, "Programs", "recollect"), "recollect.exe");
    const config = resolveConfig(
      {},
      location,
      { platform: "win32", env: { LOCALAPPDATA: local, PATH: "" } },
    );
    expect(config.binaryPath).toBe(binary);
  });

  test("fails with a clear message when nothing is found", () => {
    const home = mkdtempSync(join(tmpdir(), "rc-none-"));
    expect(() =>
      resolveConfig({}, location, { platform: "linux", env: { HOME: home, PATH: "" } }),
    ).toThrow(ConfigError);
  });
});
