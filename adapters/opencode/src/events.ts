// Converts host events into durable engine records and run boundaries.

import { Cause, Effect, Exit, Fiber, Stream } from "effect";

export const MEMORY_MARKER = "<!-- recollect:memory:";

const EVENT_TYPES = [
  "session.execution.succeeded",
  "session.execution.failed",
  "session.execution.interrupted",
  "session.compaction.started",
  "session.compaction.failed",
  "session.idle",
  "session.status",
];

const RETRY_DELAY_MS = 1_000;

export interface SubscribeOptions {
  retryDelayMs?: number;
  sessionAllowed?: (sessionID: string) => Promise<boolean>;
}

export interface HostRecord {
  id: string;
  sessionID: string;
  generation: string;
  time: string;
  role: string;
  text: string;
  synthetic: boolean;
  toolCalls?: unknown[];
}

export interface RunBoundary {
  sessionID: string;
  generation: string;
  kind: string;
  time: string;
  available: boolean;
}

function toTime(value: unknown): string {
  if (value instanceof Date) return value.toISOString();
  if (typeof value === "number") return new Date(value).toISOString();
  if (value && typeof value === "object") {
    const created = (value as { created?: unknown }).created;
    if (typeof created === "number" || typeof created === "string") return toTime(created);
  }
  if (typeof value === "string") return value;
  return new Date(0).toISOString();
}

function extractText(msg: any): string {
  if (typeof msg.text === "string") return msg.text;
  const parts = Array.isArray(msg.parts) ? msg.parts : [];
  return parts
    .filter((part: any) => part?.type === "text" && typeof part.text === "string")
    .map((part: any) => part.text)
    .join(" ")
    .replace(/\s+/g, " ")
    .trim();
}

export function normalizeMessage(msg: any): HostRecord | null {
  if (!msg || typeof msg.id !== "string" || !msg.id) return null;
  if (msg.synthetic === true) return null;
  if (msg.role === "system" || msg.role === "tool") return null;

  const text = extractText(msg);
  if (!text || !text.trim()) return null;

  return {
    id: msg.id,
    sessionID: typeof msg.sessionID === "string" ? msg.sessionID : "",
    generation: typeof msg.generation === "string" && msg.generation ? msg.generation : "default",
    time: toTime(msg.time),
    role: msg.role === "assistant" ? "assistant" : "user",
    text,
    synthetic: false,
  };
}

export function normalizeBoundary(
  source: { sessionID: string; generation?: string; kind?: string; time?: unknown },
  available: boolean,
): RunBoundary {
  return {
    sessionID: source.sessionID,
    generation: source.generation || "default",
    kind: source.kind || "terminal",
    time: toTime(source.time ?? Date.now()),
    available,
  };
}

function messageFromEvent(event: any): any | null {
  const props = event?.data ?? event?.properties ?? {};
  const candidate = props.message ?? props.info ?? (typeof props.text === "string" ? props : null);
  return candidate && typeof candidate.id === "string" ? candidate : null;
}

function boundaryFromEvent(event: any): { sessionID: string; generation?: string; kind: string } | null {
  const type = String(event?.type ?? "");
  const props = event?.data ?? event?.properties ?? {};
  if (type === "session.status") {
    if (props.status?.type !== "idle") return null;
    return { sessionID: props.sessionID ?? props.info?.sessionID ?? "", kind: "terminal" };
  }
  if (type === "session.compaction.started" || type === "session.compaction.failed") {
    const kind = type === "session.compaction.failed" ? "compaction-failed" : "compaction";
    return { sessionID: props.sessionID ?? "", kind, generation: props.generation };
  }
  if (/(succeeded|failed|idle|interrupted)$/.test(type)) {
    const kind = type.endsWith(".succeeded") ? "complete"
      : type.endsWith(".failed") ? "failed"
      : type.endsWith(".interrupted") ? "interrupted"
      : "terminal";
    return { sessionID: props.sessionID ?? props.info?.sessionID ?? "", kind };
  }
  return null;
}

function diagnosticLine(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return `recollect: event stream failed, retrying: ${message.replace(/\s+/g, " ").trim()}\n`;
}

export function subscribeEvents(
  ctx: any,
  client: { eventsIngest(params: unknown): Promise<any>; isDead?(): boolean },
  options: SubscribeOptions = {},
): Effect.Effect<Fiber.Fiber<void, unknown>> {
  const retryDelayMs = options.retryDelayMs ?? RETRY_DELAY_MS;
  const sessionAllowed = options.sessionAllowed;
  const pending: Array<{ records: HostRecord[]; boundaries: RunBoundary[] }> = [];

  // A failed ingest stays pending until a later subscription flushes it.
  const flush = (): Effect.Effect<void, unknown> =>
    Effect.gen(function* () {
      while (pending.length) {
        yield* Effect.promise(() => client.eventsIngest(pending[0]));
        pending.shift();
      }
    });

  const handle = (event: any) =>
    Effect.gen(function* () {
      const records: HostRecord[] = [];
      const boundaries: RunBoundary[] = [];
      const message = messageFromEvent(event);
      const boundary = boundaryFromEvent(event);
      const sessionID = boundary?.sessionID ??
        (message && typeof message.sessionID === "string" ? message.sessionID : "");
      if (sessionID && sessionAllowed &&
          !(yield* Effect.promise(() => sessionAllowed(sessionID)))) {
        return;
      }
      if (message) {
        const record = normalizeMessage(message);
        if (record) records.push(record);
      }
      if (boundary) {
        // An idle boundary is a liveness signal, not a capture trigger; reading
        // the full history on every turn would add cost with no engine job.
        const history = boundary.kind === "terminal"
          ? []
          : yield* Effect.promise(async () => {
              const operation = ctx.session.context({ sessionID: boundary.sessionID });
              try {
                const value = operation && typeof operation.pipe === "function"
                  ? await Effect.runPromise(operation)
                  : await operation;
                return Array.isArray(value) ? value : [];
              } catch {
                return [];
              }
            });
        for (const message of history) {
          const record = normalizeMessage(message);
          if (!record) continue;
          if (!record.sessionID) record.sessionID = boundary.sessionID;
          if (record.generation === "default" && boundary.generation) record.generation = boundary.generation;
          records.push(record);
        }
        boundaries.push(normalizeBoundary(boundary, history.length > 0));
      }
      if (records.length || boundaries.length) {
        pending.push({ records, boundaries });
        yield* flush();
      }
    });

  const consume = (): Effect.Effect<void, unknown> =>
    Effect.gen(function* () {
      yield* flush();
      const stream: Stream.Stream<any, unknown> = ctx.event.subscribe({ types: [...EVENT_TYPES] });
      yield* Stream.runForEach(stream, handle);
    });

  const supervise = (): Effect.Effect<void> =>
    Effect.gen(function* () {
      let delay = retryDelayMs;
      while (true) {
        const exit = yield* Effect.exit(consume());
        if (Exit.isSuccess(exit)) return;
        if (client.isDead?.() === true) {
          process.stderr.write("recollect: engine transport closed; event capture stopped\n");
          return;
        }
        process.stderr.write(diagnosticLine(Cause.squash(exit.cause)));
        yield* Effect.sleep(delay);
        delay = Math.min(delay * 2, 30_000);
      }
    });

  return Effect.gen(function* () {
    return Effect.runFork(supervise());
  });
}
