// OpenCode plugin entrypoint using the cancellable Effect API.

import { Plugin } from "@opencode/plugin/effect";
import { Effect, Fiber } from "effect";
import { resolveConfig } from "./config.js";
import { createRpcClient } from "./rpc.js";
import { registerMemoryTool } from "./tools.js";
import { createSessionVerifier, registerContext } from "./context.js";
import { registerCommands } from "./commands.js";
import { createSnapshotCache } from "./snapshots.js";
import { subscribeEvents } from "./events.js";
import { runGeneration, startWorker, type Generate } from "./worker.js";

function bounded(promise: Promise<unknown>, ms: number): Promise<void> {
  return new Promise<void>((resolve) => {
    const timer = setTimeout(resolve, ms);
    promise.then(
      () => { clearTimeout(timer); resolve(); },
      () => { clearTimeout(timer); resolve(); },
    );
  });
}

export const RecollectPlugin = Plugin.define({
  id: "recollect",
  effect: (ctx: any) =>
    Effect.gen(function* () {
      const config = resolveConfig(
        ctx.options ?? {},
        ctx.location,
        { platform: process.platform, env: process.env as Record<string, string | undefined> },
      );

      const generateText = ctx.experimental?.generate?.text;
      const generationState = config.generationPolicy === "off"
        ? "disabled"
        : typeof generateText === "function" ? "enabled" : "unavailable";
      const capabilities = config.capabilities.filter((capability) => capability !== "generation");
      if (generationState === "enabled") capabilities.push("generation");

      const client = createRpcClient(config.binaryPath);
      yield* Effect.addFinalizer(() =>
        Effect.promise(async () => {
          // A closed, busy, or dead engine still releases the child here.
          await bounded(client.engineClose({ deadlineMS: 5000 }), 6000);
          client.dispose();
        }),
      );

      yield* Effect.promise(() =>
        client.initialize({
          projectDir: config.projectDir,
          projectRoot: config.projectRoot,
          userDir: config.userDir,
          harnessID: config.harnessID,
          capabilities,
          generationState,
          protocol: { major: 1, minor: 1 },
        }),
      );

      const cache = createSnapshotCache(client);
      yield* Effect.addFinalizer(() => {
        cache.forget();
        return Effect.void;
      });
      const hostProject = ctx.location?.project as { id?: unknown } | undefined;
      const sessionAllowed = createSessionVerifier(ctx, {
        directory: config.directory,
        workspaceID: config.workspaceID,
        projectID: typeof hostProject?.id === "string" ? hostProject.id : "",
      });
      yield* registerMemoryTool(ctx, client);
      yield* registerContext(ctx, cache, sessionAllowed);
      yield* registerCommands(ctx, client, cache);

      const controller = new AbortController();
      const generation = generationState === "enabled";
      const generate: Generate | null = generation
        ? (prompt, signal) => runGeneration(() => generateText.call(ctx.experimental.generate, { prompt }), signal)
        : null;

      const worker = startWorker(client, generate, {
        generation,
        generationTimeoutMs: config.generationTimeoutMs,
        signal: controller.signal,
      });
      yield* Effect.addFinalizer(() =>
        Effect.promise(async () => {
          controller.abort();
          await bounded(worker.stop(), 5000);
        }),
      );

      const eventFiber = yield* subscribeEvents(ctx, client, { sessionAllowed });
      yield* Effect.addFinalizer(() => Fiber.interrupt(eventFiber));
    }),
});

export default RecollectPlugin;
