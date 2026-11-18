// Converts host events into durable engine records and run boundaries.

import { Effect, Fiber, Stream } from "effect";

export const MEMORY_MARKER = "<!-- recollect:memory:";

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
  if (text.includes(MEMORY_MARKER)) return null;

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
  if (/(succeeded|failed|idle|interrupted)$/.test(type)) {
    const kind = type.endsWith(".succeeded") ? "complete"
      : type.endsWith(".failed") ? "failed"
      : type.endsWith(".interrupted") ? "interrupted"
      : "terminal";
    return { sessionID: props.sessionID ?? props.info?.sessionID ?? "", kind };
  }
  if (type.includes("compact")) {
    return { sessionID: props.sessionID ?? "", kind: "compaction", generation: props.generation };
  }
  return null;
}

export function subscribeEvents(
  ctx: any,
  client: { eventsIngest(params: unknown): Promise<any> },
): Effect.Effect<Fiber.RuntimeFiber<void, unknown>> {
  const handle = (event: any) =>
    Effect.gen(function* () {
      const records: HostRecord[] = [];
      const boundaries: RunBoundary[] = [];
      const message = messageFromEvent(event);
      if (message) {
        const record = normalizeMessage(message);
        if (record) records.push(record);
      }
      const boundary = boundaryFromEvent(event);
      if (boundary) {
        const operation = ctx.session.context({ sessionID: boundary.sessionID });
        const history = yield* Effect.promise(async () => {
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
        yield* Effect.promise(() => client.eventsIngest({ records, boundaries }));
      }
    });

  return Effect.gen(function* () {
    const stream = ctx.event.subscribe({
      types: ["session.execution.succeeded", "session.execution.failed", "session.execution.interrupted"],
    });
    return Effect.runFork(Stream.runForEach(stream, handle));
  });
}
