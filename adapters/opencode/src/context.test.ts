// audit: behavior - Inserts one memory block per snapshot and reuses the snapshotID.
// audit: regression - Duplicate system blocks and stale snapshot replacement must not occur.
// audit: coverage - Covers append-once, idempotent reuse, and refresh on snapshot change.
// audit: seam - A fake context.get client and a capturing session hook stand in for the host.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { MEMORY_MARKER, createContextHandler, registerContext } from "./context";

function fakeClient() {
  let snapshot = { snapshotID: "snap-1", section: "MEMORY SECTION" };
  return {
    contextGet: async () => snapshot,
    setSnapshot(next: { snapshotID: string; section: string }) { snapshot = next; },
  };
}

function partText(part: unknown): string {
  if (typeof part === "string") return part;
  if (part && typeof part === "object" && typeof (part as { text?: unknown }).text === "string") {
    return (part as { text: string }).text;
  }
  return "";
}

function markerCount(system: unknown[]): number {
  return system.filter((part) => partText(part).startsWith(MEMORY_MARKER)).length;
}

describe("context hook", () => {
  test("appends exactly one block and reuses it for the same snapshot", async () => {
    const client = fakeClient();
    const handler = createContextHandler(client as any);
    const event = { system: ["You are an assistant."] } as any;

    await Effect.runPromise(handler(event));
    expect(markerCount(event.system)).toBe(1);
    expect(event.system.some((part) => typeof part === "object" && part !== null)).toBe(true);
    expect(event.system.map(partText).join("\n")).toContain("MEMORY SECTION");
    expect(client.contextGet).toBeDefined();

    await Effect.runPromise(handler(event));
    expect(markerCount(event.system)).toBe(1);
  });

  test("replaces the block when the snapshotID changes", async () => {
    const client = fakeClient();
    const handler = createContextHandler(client as any);
    const event = { system: ["You are an assistant."] } as any;

    await Effect.runPromise(handler(event));
    client.setSnapshot({ snapshotID: "snap-2", section: "FRESH SECTION" });
    await Effect.runPromise(handler(event));

    expect(markerCount(event.system)).toBe(1);
    const block = event.system.map(partText).find((text) => text.startsWith(MEMORY_MARKER));
    expect(block).toContain("snap-2");
    expect(block).toContain("FRESH SECTION");
    expect(event.system.map(partText).join("\n")).not.toContain("MEMORY SECTION");
  });

  test("registers on the context session hook", async () => {
    const seen: Array<[string, unknown]> = [];
    const ctx = { session: { hook: (name: string, handler: unknown) => { seen.push([name, handler]); return Effect.void; } } };
    await Effect.runPromise(registerContext(ctx as any, fakeClient() as any));
    expect(seen).toHaveLength(1);
    expect(seen[0][0]).toBe("context");
  });
});
