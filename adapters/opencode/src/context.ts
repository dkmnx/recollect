import { Effect } from "effect";
import { MEMORY_MARKER } from "./events";
import type { SnapshotCache } from "./snapshots";
export { MEMORY_MARKER } from "./events";

export type Verifier = (sessionID: string, projectID?: string) => Promise<boolean>;

function runHost<T>(value: unknown): Promise<T> {
  if (value && typeof (value as { pipe?: unknown }).pipe === "function") {
    return Effect.runPromise(value as Effect.Effect<T>);
  }
  return Promise.resolve(value as T);
}

function normalizedDirectory(value: string): string {
  const resolved = process.platform === "win32" ? value.replace(/\//g, "\\").toLowerCase() : value;
  return resolved.endsWith("/") || resolved.endsWith("\\") ? resolved.slice(0, -1) : resolved;
}

export function createSessionVerifier(
  ctx: any,
  bound: { directory: string; workspaceID: string; projectID?: string },
): Verifier {
  return async (sessionID, projectID) => {
    try {
      if (!ctx?.session?.get || !sessionID) return false;
      const info = await runHost<any>(ctx.session.get({ sessionID }));
      if (!info || typeof info !== "object") {
        process.stderr.write("recollect: session verification gap\n");
        return false;
      }
      if (info.parentID) return false;
      const expectedProject = bound.projectID || projectID || "";
      if (expectedProject && typeof info.projectID === "string" && info.projectID !== expectedProject) return false;
      const location = info.location ?? {};
      const directory = typeof location.directory === "string" ? location.directory : "";
      if (bound.directory && directory &&
          normalizedDirectory(directory) !== normalizedDirectory(bound.directory)) return false;
      const workspaceID = typeof location.workspaceID === "string" ? location.workspaceID : "";
      if (bound.workspaceID && workspaceID && workspaceID !== bound.workspaceID) return false;
      return true;
    } catch {
      process.stderr.write("recollect: session verification gap\n");
      return false;
    }
  };
}

export function createContextHandler(cache: SnapshotCache, verify: Verifier) {
  return (event: any) =>
    Effect.gen(function* () {
      const sessionID = typeof event?.sessionID === "string" ? event.sessionID : "";
      if (!sessionID) return event;
      const projectID = typeof event?.projectID === "string" ? event.projectID : "";
      if (!(yield* Effect.promise(() => verify(sessionID, projectID)))) return event;

      let block = cache.block(sessionID);
      if (!block) block = yield* Effect.promise(() => cache.ensure(sessionID));
      if (!block) return event;

      if (!Array.isArray(event.system)) event.system = [];
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
  cache: SnapshotCache,
  verify: Verifier,
): Effect.Effect<void> {
  return Effect.gen(function* () {
    yield* (ctx.session.hook("context", createContextHandler(cache, verify)) as Effect.Effect<void>);
  });
}
