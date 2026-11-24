// Registers Recollect user commands through the command editor.

import { Effect } from "effect";

export function registerCommands(
  ctx: any,
  client: { statusGet(params?: unknown): Promise<any> },
  cache: { refresh(sessionID: string): Promise<string | null> },
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* (ctx.command.transform((commands: any) => {
      commands.add({
        name: "recollect-status",
        description: "Show Recollect memory health and pending work.",
        execute: async () => {
          const status = await client.statusGet({});
          const parts = [
            `Recollect: ${status.health}`,
            `pending ${status.pending ?? 0}`,
            `conflicts ${status.conflicts ?? 0}`,
            `generation ${status.generationState ?? "unknown"}`,
          ];
          if (status.lastErrorCode) parts.push(`last error ${status.lastErrorCode}`);
          return parts.join("; ");
        },
      });

      commands.add({
        name: "recollect-refresh",
        description: "Refresh the Recollect memory context snapshot.",
        execute: async (invocation: any) => {
          const sessionID = typeof invocation?.sessionID === "string" ? invocation.sessionID : "";
          if (!sessionID) return "Memory refresh needs a session.";
          const block = await cache.refresh(sessionID);
          return block ? `Memory refreshed for ${sessionID}.` : "Memory refresh failed; kept the last snapshot.";
        },
      });
    })) as Effect.Effect<void>;
  });
}
