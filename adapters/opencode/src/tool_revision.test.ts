// audit: the memory tool keeps the read revision in the model-visible result.
// regression: dropping the revision made every follow-up correction fail with read_required.
// coverage: existing tool tests only return content, so the revision was never observed.
// seam: memoryToolDefinition.execute with a fake RPC client that returns a revision.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { memoryToolDefinition } from "./tools";

describe("memory tool revision", () => {
  test("returns the revision from a read result", async () => {
    const client = {
      memoryExecute: async () => ({ content: "# fact", revision: "rev-123" }),
    };
    const result: any = await Effect.runPromise(
      memoryToolDefinition.execute({ target: "project", operation: "read", name: "fact" }, client) as any,
    );
    expect(result.revision).toBe("rev-123");
    expect(result.content).toContain("rev-123");
  });
});
