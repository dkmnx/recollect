// audit: behavior - Normalizes host messages into HostRecord and drops synthetic control text.
// audit: regression - Memory context and system text must never become user-authored facts.
// audit: coverage - Covers stable IDs, roles, parts, and unavailable history boundaries.
// audit: seam - Pure normalization functions sit at the host-event to engine-record seam.

import { describe, expect, test } from "bun:test";
import { Effect, Fiber, Stream } from "effect";
import { MEMORY_MARKER, normalizeBoundary, normalizeMessage, subscribeEvents } from "./events";

describe("subscribeEvents", () => {
  test("uses the Effect stream signature and forks the consumer", async () => {
    let types: readonly string[] | undefined;
    const ctx = {
      event: {
        subscribe: (input: { types?: readonly string[] }) => {
          types = input.types;
          return Stream.empty;
        },
      },
      session: { context: () => Effect.succeed([]) },
    };
    const client = { eventsIngest: async () => ({}) };
    const fiber = await Effect.runPromise(subscribeEvents(ctx, client));
    expect(fiber).toBeDefined();
    await Effect.runPromise(Fiber.interrupt(fiber));
    expect(types).toContain("session.execution.succeeded");
    expect(types).not.toContain("message.updated");
  });
  test("reads terminal session IDs from the host data field", async () => {
    const seen: any[] = [];
    const ctx = {
      event: {
        subscribe: () =>
          Stream.succeed({
            type: "session.execution.succeeded",
            data: { sessionID: "ses-1" },
          }),
      },
      session: { context: () => Effect.succeed([]) },
    };
    const client = { eventsIngest: async (params: any) => { seen.push(params); return {}; } };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client));
    await Effect.runPromise(Fiber.join(fiber));
    expect(seen[0].boundaries[0].sessionID).toBe("ses-1");
  });

  test("ingests terminal boundaries from the effect stream", async () => {
    const seen: any[] = [];
    const ctx = {
      event: {
        subscribe: () =>
          Stream.succeed({ type: "session.execution.succeeded", properties: { sessionID: "s1" } }),
      },
      session: {
        context: () =>
          Effect.succeed([{ id: "m1", sessionID: "s1", role: "user", text: "remember this" }]),
      },
    };
    const client = { eventsIngest: async (params: any) => { seen.push(params); return {}; } };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client));
    await Effect.runPromise(Fiber.join(fiber));
    expect(seen).toHaveLength(1);
    expect(seen[0].boundaries[0].kind).toBe("complete");
    expect(seen[0].records).toHaveLength(1);
    expect(seen[0].records[0].id).toBe("m1");
  });
});

describe("normalizeMessage", () => {
  test("preserves a stable ID, role, session, and text", () => {
    const record = normalizeMessage({
      id: "msg-42",
      sessionID: "ses-1",
      role: "user",
      generation: "g2",
      time: "2026-11-18T00:00:00.000Z",
      text: "Remember the build flag.",
    });
    expect(record).not.toBeNull();
    expect(record!.id).toBe("msg-42");
    expect(record!.sessionID).toBe("ses-1");
    expect(record!.role).toBe("user");
    expect(record!.text).toBe("Remember the build flag.");
    expect(record!.generation).toBe("g2");
  });

  test("reads text from parts when no plain text is present", () => {
    const record = normalizeMessage({
      id: "msg-7",
      sessionID: "ses-1",
      role: "assistant",
      parts: [
        { type: "text", text: "Hello " },
        { type: "tool", text: "ignored" },
        { type: "text", text: "world" },
      ],
    });
    expect(record!.text).toBe("Hello world");
  });

  test("defaults the generation when the host omits it", () => {
    const record = normalizeMessage({ id: "m", sessionID: "s", role: "user", text: "x" });
    expect(record!.generation).toBe("default");
  });

  test("drops synthetic and control-role records", () => {
    expect(normalizeMessage({ id: "m1", sessionID: "s", role: "user", text: "x", synthetic: true })).toBeNull();
    expect(normalizeMessage({ id: "m2", sessionID: "s", role: "system", text: "x" })).toBeNull();
    expect(normalizeMessage({ id: "m3", sessionID: "s", role: "tool", text: "x" })).toBeNull();
  });

  test("drops memory-generated context and empty text", () => {
    const injected = MEMORY_MARKER + "snap-1 -->\nMEMORY SECTION";
    expect(normalizeMessage({ id: "m4", sessionID: "s", role: "user", text: injected })).toBeNull();
    expect(normalizeMessage({ id: "m5", sessionID: "s", role: "user", text: "   " })).toBeNull();
  });
});

describe("normalizeBoundary", () => {
  test("marks a missing history range as unavailable", () => {
    const boundary = normalizeBoundary({ sessionID: "ses-1", generation: "g2", kind: "terminal" }, false);
    expect(boundary.sessionID).toBe("ses-1");
    expect(boundary.generation).toBe("g2");
    expect(boundary.available).toBe(false);
  });
});
