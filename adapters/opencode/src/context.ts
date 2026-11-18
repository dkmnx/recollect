// Injects the engine memory section once per snapshot.

import { Effect } from "effect";
import { MEMORY_MARKER } from "./events.js";
export { MEMORY_MARKER } from "./events.js";

interface Snapshot {
  snapshotID: string;
  section: string;
}

export function createContextHandler(
  client: { contextGet(params: unknown): Promise<Snapshot> },
) {
  return (event: any) =>
    Effect.gen(function* () {
      const snapshot = yield* Effect.promise(() => client.contextGet({ refresh: false }));
      if (!snapshot?.snapshotID || !snapshot?.section) return event;

      if (!Array.isArray(event.system)) event.system = [];
      const block = `${MEMORY_MARKER}${snapshot.snapshotID} -->\n${snapshot.section}`;
      const textOf = (part: unknown) =>
        typeof part === "string"
          ? part
          : part && typeof part === "object" && typeof (part as { text?: unknown }).text === "string"
            ? (part as { text: string }).text
            : "";
      const index = event.system.findIndex((part: unknown) => textOf(part).startsWith(MEMORY_MARKER));
      const textPart = { type: "text", text: block };

      if (index >= 0) {
        if (textOf(event.system[index]) !== block) event.system[index] = textPart;
      } else {
        event.system.push(textPart);
      }
      return event;
    });
}

export function registerContext(
  ctx: any,
  client: { contextGet(params: unknown): Promise<Snapshot> },
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* ctx.session.hook("context", createContextHandler(client));
  });
}
