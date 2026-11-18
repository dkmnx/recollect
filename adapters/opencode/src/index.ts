// OpenCode plugin entrypoint using the cancellable Effect API.

import { Plugin } from "@opencode/plugin/effect";
import { Effect, Fiber } from "effect";
import { resolveConfig } from "./config.js";
import { createRpcClient } from "./rpc.js";
import { registerMemoryTool } from "./tools.js";
import { registerContext } from "./context.js";
import { registerCommands } from "./commands.js";
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
          userDir: config.userDir,
          harnessID: config.harnessID,
          capabilities: config.capabilities,
          protocol: { major: 1, minor: 0 },
        }),
      );

      yield* registerMemoryTool(ctx, client);
      yield* registerContext(ctx, client);
      yield* registerCommands(ctx, client);

      const controller = new AbortController();
      const generateText = ctx.experimental?.generate?.text;
      const generation = typeof generateText === "function";
      const generate: Generate | null = generation
        ? (prompt, signal) => runGeneration(() => generateText.call(ctx.experimental.generate, { prompt }), signal)
        : null;

      const worker = startWorker(client, generate, { generation, signal: controller.signal });
      yield* Effect.addFinalizer(() =>
        Effect.promise(async () => {
          controller.abort();
          await bounded(worker.stop(), 5000);
        }),
      );

      const eventFiber = yield* subscribeEvents(ctx, client);
      yield* Effect.addFinalizer(() => Fiber.interrupt(eventFiber));
    }),
});

export default RecollectPlugin;
