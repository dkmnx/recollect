// Registers Recollect user commands through the command editor.

import { Cause, Effect } from "effect";

function hostEffect<T>(value: unknown): Effect.Effect<T, unknown> {
  if (value && typeof (value as { pipe?: unknown }).pipe === "function") {
    return value as Effect.Effect<T>;
  }
  if (value && typeof (value as { then?: unknown }).then === "function") {
    return Effect.tryPromise(() => value as Promise<T>);
  }
  return Effect.succeed(value as T);
}

function publish(ctx: any, sessionID: string, description: string): Effect.Effect<void, unknown> {
  if (!sessionID) return Effect.void;
  // OpenCode renders description in the TUI and sends text to the model as user input.
  // Keep text empty so the notice is display-only.
  return Effect.try(() => ctx.session.synthetic({ sessionID, description, text: "", resume: false })).pipe(
    Effect.flatMap((value) => hostEffect<void>(value)),
    Effect.catchCause((cause) => Cause.hasInterruptsOnly(cause) ? Effect.failCause(cause) : Effect.void),
  );
}

function settle<T>(work: () => Promise<T>): Effect.Effect<T | null> {
  return Effect.tryPromise({ try: work, catch: () => null }).pipe(
    Effect.orElseSucceed(() => null),
  );
}

function formatStatus(status: any): string {
  const parts = [
    `${status.health}`,
    `pending ${status.pending ?? 0}`,
    `conflicts ${status.conflicts ?? 0}`,
    `generation ${status.generationState ?? "unknown"}`,
  ];
  if (status.lastErrorCode) parts.push(`last error ${status.lastErrorCode}`);
  return parts.join("; ");
}

export function registerCommands(
  ctx: any,
  client: { statusGet(params?: unknown): Promise<any> },
  cache: { refresh(sessionID: string): Promise<string | null> },
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* hostEffect<void>(ctx.command.transform((commands: any) => {
      commands.add({
        name: "recollect-status",
        description: "Show Recollect memory health and pending work.",
        execute: (invocation: any) =>
          Effect.gen(function* () {
            const sessionID = typeof invocation?.sessionID === "string" ? invocation.sessionID : "";
            if (!sessionID) return;
            const status = yield* settle(() => client.statusGet({}));
            const text = status ? formatStatus(status) : "unavailable (status request failed)";
            yield* publish(ctx, sessionID, `Recollect status: ${text}`);
          }),
      });

      commands.add({
        name: "recollect-refresh",
        description: "Refresh the Recollect memory context snapshot.",
        execute: (invocation: any) =>
          Effect.gen(function* () {
            const sessionID = typeof invocation?.sessionID === "string" ? invocation.sessionID : "";
            if (!sessionID) return;
            const block = yield* settle(() => cache.refresh(sessionID));
            const text = block
              ? `Memory refreshed for ${sessionID}.`
              : "Memory refresh failed; kept the last snapshot.";
            yield* publish(ctx, sessionID, `Recollect refresh: ${text}`);
          }),
      });
    })).pipe(Effect.catch(() => Effect.void));
  });
}
