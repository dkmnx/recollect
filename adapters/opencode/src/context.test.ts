// audit: behavior - Injects one cached memory block per verified host session and skips child sessions.
// audit: regression - A per-connection snapshot bled across sessions and captured child run boundaries.
// audit: coverage - Verified, child, and mismatched sessions, plus one marked block per request.
// audit: seam - Fake host session.get, context hook events, and an in-memory snapshot cache.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { MEMORY_MARKER, createContextHandler, createSessionVerifier, registerContext } from "./context";
import { createSnapshotCache } from "./snapshots";

function fakeClient() {
  const calls: any[] = [];
  const snapshot = { snapshotID: "snap-1", section: "MEMORY SECTION" };
  return {
    calls,
    contextGet: async (params: any) => {
      calls.push(params);
      return snapshot;
    },
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

const bound = { directory: "/proj", workspaceID: "" };

function host(info: any) {
  return { session: { get: (_params: any) => Effect.succeed(info) } };
}

describe("context hook", () => {
  test("injects one cached block for a verified session", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);
    const handler = createContextHandler(cache, async () => true);
    const event = { sessionID: "s1", system: ["You are an assistant."] } as any;

    await Effect.runPromise(handler(event));
    await Effect.runPromise(handler(event));

    expect(markerCount(event.system)).toBe(1);
    expect(client.calls).toEqual([{ refresh: true }]);
    expect(event.system.map(partText).join("\n")).toContain("MEMORY SECTION");
  });

  test("skips a child session and reads no snapshot", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);
    const verify = createSessionVerifier(
      host({ projectID: "p", location: { directory: "/proj" }, parentID: "parent" }) as any,
      bound,
    );
    const handler = createContextHandler(cache, verify);
    const event = { sessionID: "child", system: [] } as any;

    await Effect.runPromise(handler(event));

    expect(markerCount(event.system)).toBe(0);
    expect(client.calls).toEqual([]);
  });

  test("skips a session from another location", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);
    const verify = createSessionVerifier(
      host({ projectID: "p", location: { directory: "/other" } }) as any,
      bound,
    );
    const handler = createContextHandler(cache, verify);
    const event = { sessionID: "other", system: [] } as any;

    await Effect.runPromise(handler(event));

    expect(markerCount(event.system)).toBe(0);
    expect(client.calls).toEqual([]);
  });

  test("skips a session from another project", async () => {
    const client = fakeClient();
    const cache = createSnapshotCache(client as any);
    const verify = createSessionVerifier(
      host({ projectID: "other-project", location: { directory: "/proj" } }) as any,
      bound,
    );
    const handler = createContextHandler(cache, verify);
    const event = { sessionID: "s1", projectID: "p", system: [] } as any;

    await Effect.runPromise(handler(event));

    expect(markerCount(event.system)).toBe(0);
    expect(client.calls).toEqual([]);
  });

  test("registers on the context session hook", async () => {
    const seen: Array<[string, unknown]> = [];
    const ctx = {
      session: {
        hook: (name: string, handler: unknown) => { seen.push([name, handler]); return Effect.void; },
      },
    };
    const cache = createSnapshotCache(fakeClient() as any);

    await Effect.runPromise(registerContext(ctx as any, cache, async () => true));

    expect(seen).toHaveLength(1);
    expect(seen[0][0]).toBe("context");
  });
});
