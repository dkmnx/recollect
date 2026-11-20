// audit: behavior - Resolves plugin options into bound engine roots with platform-specific defaults.
// audit: regression - A missing binaryPath or wrong OS root must fail loudly, never download.
// audit: coverage - Covers host inputs that the real OpenCode ctx supplies at load time.
// audit: seam - The host/platform seam is injected so Windows and POSIX resolve without a real host.

import { describe, expect, test } from "bun:test";
import { ConfigError, computeUserRoot, resolveConfig } from "./config";

describe("computeUserRoot", () => {
  test("uses LOCALAPPDATA/recollect on Windows", () => {
    expect(computeUserRoot("win32", { LOCALAPPDATA: "C:\\Users\\x\\AppData\\Local" })).toBe(
      "C:\\Users\\x\\AppData\\Local\\recollect",
    );
  });

  test("falls back to USERPROFILE when LOCALAPPDATA is absent", () => {
    expect(computeUserRoot("win32", { USERPROFILE: "C:\\Users\\x" })).toBe(
      "C:\\Users\\x\\AppData\\Local\\recollect",
    );
  });

  test("uses ~/.recollect on Linux and macOS", () => {
    expect(computeUserRoot("linux", { HOME: "/home/u" })).toBe("/home/u/.recollect");
    expect(computeUserRoot("darwin", { HOME: "/Users/u" })).toBe("/Users/u/.recollect");
  });

  test("uses a literal ~ home when HOME is missing on POSIX", () => {
    expect(computeUserRoot("linux", {})).toBe("~/.recollect");
  });
});

describe("resolveConfig", () => {
  const location = { project: { canonical: "C:\\work\\demo" }, workspaceID: "ws-1" };

  test("requires an explicit binaryPath", () => {
    expect(() =>
      resolveConfig({}, location, { platform: "win32", env: { LOCALAPPDATA: "C:\\AppData\\Local" } }),
    ).toThrow(ConfigError);
  });

  test("binds the canonical project root and the default user root", () => {
    const config = resolveConfig(
      { binaryPath: "C:\\tools\\recollect.exe" },
      location,
      { platform: "win32", env: { LOCALAPPDATA: "C:\\AppData\\Local" } },
    );
    expect(config.binaryPath).toBe("C:\\tools\\recollect.exe");
    expect(config.projectRoot).toBe("C:\\work\\demo");
    expect(config.projectDir).not.toBe("C:\\work\\demo");
    expect(config.userDir).toBe("C:\\AppData\\Local\\recollect");
    expect(config.workspaceID).toBe("ws-1");
  });

  test("lets an explicit userDir override the default", () => {
    const config = resolveConfig(
      { binaryPath: "/opt/recollect", userDir: "/srv/memory" },
      { project: { canonical: "/work/demo" } },
      { platform: "linux", env: { HOME: "/home/u" } },
    );
    expect(config.userDir).toBe("/srv/memory");
    expect(config.projectRoot).toBe("/work/demo");
    expect(config.projectDir).not.toBe("/work/demo");
  });

  test("defaults generation policy to host and timeout to 120000", () => {
    const config = resolveConfig(
      { binaryPath: "/opt/recollect" },
      { project: { canonical: "/work/demo" } },
      { platform: "linux", env: { HOME: "/home/u" } },
    );
    expect(config.generationPolicy).toBe("host");
    expect(config.generationTimeoutMs).toBe(120000);
  });

  test("accepts an explicit off policy and a bounded timeout", () => {
    const config = resolveConfig(
      { binaryPath: "/opt/recollect", generationPolicy: "off", generationTimeoutMs: 1000 },
      { project: { canonical: "/work/demo" } },
      { platform: "linux", env: { HOME: "/home/u" } },
    );
    expect(config.generationPolicy).toBe("off");
    expect(config.generationTimeoutMs).toBe(1000);
  });

  test("rejects an unknown generation policy", () => {
    expect(() =>
      resolveConfig(
        { binaryPath: "/opt/recollect", generationPolicy: "always" },
        { project: { canonical: "/work/demo" } },
        { platform: "linux", env: { HOME: "/home/u" } },
      ),
    ).toThrow(ConfigError);
  });

  test("rejects a timeout outside 1 through 120000", () => {
    for (const generationTimeoutMs of [0, -1, 120001, 1.5]) {
      expect(() =>
        resolveConfig(
          { binaryPath: "/opt/recollect", generationTimeoutMs },
          { project: { canonical: "/work/demo" } },
          { platform: "linux", env: { HOME: "/home/u" } },
        ),
      ).toThrow(ConfigError);
    }
  });
});
