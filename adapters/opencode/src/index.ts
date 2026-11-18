// OpenCode plugin entrypoint using the cancellable Effect API.

import { Plugin } from "@opencode/plugin/effect";
import { Effect, Fiber } from "effect";
import { resolveConfig } from "./config.js";
import { createRpcClient } from "./rpc.js";
import { registerMemoryTool } from "./tools.js";
import { registerContext } from "./context.js";
import { registerCommands } from "./commands.js";
import { subscribeEvents } from "./events.js";
import { startWorker, type Generate } from "./worker.js";

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
          try {
            await client.engineClose({ deadlineMS: 5000 });
          } catch {
            // A closed or busy engine still releases the child here.
          }
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
        ? async (prompt) => {
            const result = await generateText.call(ctx.experimental.generate, { prompt });
            return typeof result === "string" ? result : (result?.text ?? "");
          }
        : null;

      const worker = startWorker(client, generate, { generation, signal: controller.signal });
      yield* Effect.addFinalizer(() =>
        Effect.sync(() => {
          controller.abort();
          worker.stop();
        }),
      );

      const eventFiber = yield* subscribeEvents(ctx, client);
      yield* Effect.addFinalizer(() => Fiber.interrupt(eventFiber));
    }),
});

export default RecollectPlugin;
