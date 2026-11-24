// audit: behavior - Command executors return host Effects and publish notices whose description carries the visible result.
// audit: regression - A Promise executor crashes the host, a rejected notice escapes the error channel, and a result in text is invisible in the TUI.
// audit: coverage - Covers both commands, notice labels, RPC and refresh failures, rejected notices, and missing session IDs.
// audit: seam - The command editor and session synthetic API are injected fakes.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { registerCommands } from "./commands";

interface HarnessOptions {
  statusGet?: () => Promise<any>;
  refresh?: (sessionID: string) => Promise<string | null>;
  synthetic?: (input: any) => unknown;
}

function harness(options: HarnessOptions = {}) {
  const registered: any[] = [];
  const messages: any[] = [];
  const calls: string[] = [];
  const ctx = {
    command: {
      transform: (callback: (editor: any) => void) => {
        callback({ add: (definition: any) => registered.push(definition) });
        return Effect.void;
      },
    },
    session: {
      synthetic: options.synthetic ?? ((input: any) => {
        messages.push(input);
        return Effect.void;
      }),
    },
  };
  const client = {
    statusGet: async () => {
      calls.push("statusGet");
      return options.statusGet ? options.statusGet() : { health: "ok" };
    },
  };
  const cache = {
    refresh: async (sessionID: string) => {
      calls.push(`refresh:${sessionID}`);
      return options.refresh ? options.refresh(sessionID) : null;
    },
  };
  return { ctx, client, cache, registered, messages, calls };
}

async function commandNamed(name: string, options: HarnessOptions = {}) {
  const h = harness(options);
  await Effect.runPromise(registerCommands(h.ctx, h.client, h.cache));
  const found = h.registered.find((command) => command.name === name);
  if (!found) throw new Error(`missing command ${name}`);
  return { ...h, command: found };
}

describe("registerCommands", () => {
  test("executors are runnable Effects, not Promises", async () => {
    const h = harness();
    await Effect.runPromise(registerCommands(h.ctx, h.client, h.cache));
    expect(h.registered.map((command) => command.name)).toEqual([
      "recollect-status",
      "recollect-refresh",
    ]);

    for (const command of h.registered) {
      const result = command.execute({ sessionID: "ses-1" });
      expect(typeof result?.pipe).toBe("function");
      await Effect.runPromise(result);
    }
  });

  test("status publishes the visible report without waking the model", async () => {
    const h = await commandNamed("recollect-status", {
      statusGet: async () => ({
        health: "ok",
        pending: 2,
        conflicts: 1,
        generationState: "enabled",
        lastErrorCode: "storage_failure",
        secret: "do not leak",
      }),
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-1" }));

    expect(h.messages).toHaveLength(1);
    expect(h.messages[0].sessionID).toBe("ses-1");
    expect(h.messages[0].resume).toBe(false);
    expect(h.messages[0].text).toBe("");
    expect(h.messages[0].description).toContain("Recollect status");
    expect(h.messages[0].description).toContain("ok");
    expect(h.messages[0].description).toContain("pending 2");
    expect(h.messages[0].description).toContain("generation enabled");
    expect(h.messages[0].description).toContain("last error storage_failure");
    expect(h.messages[0].description).not.toContain("do not leak");
  });

  test("refresh publishes the visible result without waking the model", async () => {
    const h = await commandNamed("recollect-refresh", {
      refresh: async () => "memory block",
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-2" }));

    expect(h.messages[0].sessionID).toBe("ses-2");
    expect(h.messages[0].resume).toBe(false);
    expect(h.messages[0].text).toBe("");
    expect(h.messages[0].description).toContain("Recollect refresh");
    expect(h.messages[0].description).toContain("Memory refreshed");
  });

  test("a failed status request publishes a bounded unavailable notice", async () => {
    const h = await commandNamed("recollect-status", {
      statusGet: async () => { throw new Error("transport secret detail"); },
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-3" }));

    expect(h.messages[0].description).toBe("Recollect status: unavailable (status request failed)");
    expect(h.messages[0].description).not.toContain("transport secret detail");
  });

  test("a failed refresh publishes the kept-snapshot notice", async () => {
    const h = await commandNamed("recollect-refresh", {
      refresh: async () => null,
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-4" }));

    expect(h.messages[0].description).toContain("Recollect refresh");
    expect(h.messages[0].description).toContain("Memory refresh failed; kept the last snapshot.");
  });

  test("a rejected notice does not fail the command", async () => {
    const h = await commandNamed("recollect-refresh", {
      synthetic: async () => { throw new Error("session gone"); },
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-5" }));
  });

  test("a failing Effect notice does not fail the command", async () => {
    const h = await commandNamed("recollect-refresh", {
      synthetic: () => Effect.fail(new Error("session gone")),
    });

    await Effect.runPromise(h.command.execute({ sessionID: "ses-6" }));
  });

  test("a missing sessionID skips the engine call and publishes nothing", async () => {
    for (const name of ["recollect-status", "recollect-refresh"]) {
      const h = await commandNamed(name);
      await Effect.runPromise(h.command.execute({}));
      expect(h.messages).toHaveLength(0);
      expect(h.calls).toHaveLength(0);
    }
  });
});
