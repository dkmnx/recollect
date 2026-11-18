// Registers Recollect user commands through the command editor.

import { Effect } from "effect";

export function registerCommands(
  ctx: any,
  client: {
    statusGet(params?: unknown): Promise<any>;
    contextGet(params: unknown): Promise<any>;
  },
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* ctx.command.transform((commands: any) => {
      commands.add({
        name: "recollect-status",
        description: "Show Recollect memory health and pending work.",
        execute: async () => {
          const status = await client.statusGet({});
          return `Recollect: ${status.health}; pending ${status.pending ?? 0}`;
        },
      });

      commands.add({
        name: "recollect-refresh",
        description: "Refresh the Recollect memory context snapshot.",
        execute: async () => {
          const snapshot = await client.contextGet({ refresh: true });
          return `Memory refreshed (${snapshot.snapshotID}).`;
        },
      });
    });
  });
}
