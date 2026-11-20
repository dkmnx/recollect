// audit: the adapter binds project memory under the user root and passes the repository root separately.
// regression: using the repository root as the memory store let search read project Markdown and append overwrite MEMORY.md.
// coverage: existing config tests assert the old root binding only.
// seam: resolveConfig with injected host location and platform.

import { describe, expect, test } from "bun:test";
import { resolveConfig } from "./config";

describe("project memory root", () => {
  test("keeps the repository root out of the memory store", () => {
    const config = resolveConfig(
      { binaryPath: "/opt/recollect", userDir: "/srv/memory" },
      { project: { canonical: "/work/demo" }, directory: "/work/demo" },
      { platform: "linux", env: {} },
    );
    expect(config.projectRoot).toBe("/work/demo");
    expect(config.projectDir).not.toBe("/work/demo");
    expect(config.projectDir.startsWith("/srv/memory/")).toBe(true);
    expect(config.directory).toBe("/work/demo");
  });
});
