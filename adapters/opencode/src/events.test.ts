// audit: behavior - Normalizes host records, maps compaction and idle boundaries, retries a failed ingest, and resubscribes after failure.
// audit: regression - Memory context, system text, dropped stream errors, and dead transports must not lose events or spin forever.
// audit: coverage - Covers normalization, compaction kinds, idle status and history skip, pending-ingest retry before resubscribe, and a dead-client stop; not repeated failures.
// audit: seam - Pure normalization plus a fake Effect stream sit at the host-event to engine-record seam.

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
    expect(types).toContain("session.compaction.started");
    expect(types).toContain("session.compaction.failed");
    expect(types).toContain("session.idle");
    expect(types).toContain("session.status");
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

  test("maps compaction start and failure to distinct boundary kinds", async () => {
    const seen: any[] = [];
    const ctx = {
      event: {
        subscribe: () =>
          Stream.make(
            { type: "session.compaction.started", properties: { sessionID: "s1" } },
            { type: "session.compaction.failed", properties: { sessionID: "s1" } },
          ),
      },
      session: {
        context: () => Effect.succeed([
          { id: "m1", sessionID: "s1", role: "user", text: "pre-shrink history" },
        ]),
      },
    };
    const client = { eventsIngest: async (params: any) => { seen.push(params); return {}; } };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client));
    await Effect.runPromise(Fiber.join(fiber));

    expect(seen.map((entry) => entry.boundaries[0].kind)).toEqual(["compaction", "compaction-failed"]);
    expect(seen[0].records[0].text).toBe("pre-shrink history");
    expect(seen[0].boundaries[0].available).toBe(true);
  });

  test("maps a session.status idle event to a terminal boundary", async () => {
    const seen: any[] = [];
    const ctx = {
      event: {
        subscribe: () => Stream.succeed({
          type: "session.status",
          properties: { sessionID: "s1", status: { type: "idle" } },
        }),
      },
      session: { context: () => Effect.succeed([]) },
    };
    const client = { eventsIngest: async (params: any) => { seen.push(params); return {}; } };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client));
    await Effect.runPromise(Fiber.join(fiber));

    expect(seen[0].boundaries[0].kind).toBe("terminal");
  });

  test("does not read history for an idle boundary", async () => {
    let contextReads = 0;
    const ctx = {
      event: {
        subscribe: () => Stream.succeed({ type: "session.idle", properties: { sessionID: "s1" } }),
      },
      session: {
        context: () => {
          contextReads += 1;
          return Effect.succeed([]);
        },
      },
    };
    const client = { eventsIngest: async () => ({}) };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client));
    await Effect.runPromise(Fiber.join(fiber));

    expect(contextReads).toBe(0);
  });

  test("resubscribes after a stream failure and keeps ingesting", async () => {
    let subscriptions = 0;
    const ctx = {
      event: {
        subscribe: () => {
          subscriptions += 1;
          return subscriptions === 1
            ? Stream.fail(new Error("event stream broke"))
            : Stream.succeed({ type: "session.idle", properties: { sessionID: "ses-retry" } });
        },
      },
      session: { context: () => Effect.succeed([]) },
    };
    const seen: any[] = [];
    const client = { eventsIngest: async (params: any) => { seen.push(params); return {}; } };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client, { retryDelayMs: 1 }));
    await Effect.runPromise(Fiber.join(fiber));

    expect(subscriptions).toBe(2);
    expect(seen).toHaveLength(1);
    expect(seen[0].boundaries[0].sessionID).toBe("ses-retry");
  });

  test("resubscribes after a handler error and keeps ingesting", async () => {
    let subscriptions = 0;
    let ingests = 0;
    const ctx = {
      event: {
        subscribe: () => {
          subscriptions += 1;
          return Stream.succeed({ type: "session.idle", properties: { sessionID: `ses-${subscriptions}` } });
        },
      },
      session: { context: () => Effect.succeed([]) },
    };
    const seen: any[] = [];
    const client = {
      eventsIngest: async (params: any) => {
        ingests += 1;
        if (ingests === 1) throw new Error("ingest failed");
        seen.push(params);
        return {};
      },
    };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client, { retryDelayMs: 1 }));
    await Effect.runPromise(Fiber.join(fiber));

    expect(subscriptions).toBe(2);
    expect(seen).toHaveLength(2);
    expect(seen[0].boundaries[0].sessionID).toBe("ses-1");
    expect(seen[1].boundaries[0].sessionID).toBe("ses-2");
  });

  test("flushes a failed ingest before the next subscription", async () => {
    let subscriptions = 0;
    const calls: any[] = [];
    const ctx = {
      event: {
        subscribe: () => {
          subscriptions += 1;
          return subscriptions === 1
            ? Stream.succeed({ type: "session.idle", properties: { sessionID: "ses-pending" } })
            : Stream.empty;
        },
      },
      session: { context: () => Effect.succeed([]) },
    };
    const client = {
      eventsIngest: async (params: any) => {
        calls.push(params);
        if (calls.length === 1) throw new Error("ingest failed");
        return {};
      },
    };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client, { retryDelayMs: 1 }));
    await Effect.runPromise(Fiber.join(fiber));

    expect(subscriptions).toBe(2);
    expect(calls).toHaveLength(2);
    expect(calls[1]).toEqual(calls[0]);
  });

  test("stops resubscribing when the client transport is dead", async () => {
    let subscriptions = 0;
    const ctx = {
      event: {
        subscribe: () => {
          subscriptions += 1;
          return Stream.fail(new Error("engine exited"));
        },
      },
      session: { context: () => Effect.succeed([]) },
    };
    const client = {
      eventsIngest: async () => { throw new Error("engine exited"); },
      isDead: () => true,
    };
    const fiber = await Effect.runPromise(subscribeEvents(ctx as any, client, { retryDelayMs: 1 }));
    await Effect.runPromise(Fiber.join(fiber));
    expect(subscriptions).toBe(1);
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

  test("keeps genuine user messages that quote a marker", () => {
    const quoted = MEMORY_MARKER + "snap-1 -->\nMEMORY SECTION";
    const record = normalizeMessage({ id: "m4", sessionID: "s", role: "user", text: quoted });
    expect(record).not.toBeNull();
    expect(record!.text).toContain(MEMORY_MARKER);
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
