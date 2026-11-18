// Leases engine jobs and runs host generation without a conversation turn.

import { Effect, Fiber } from "effect";

export interface WorkerOptions {
  generation?: boolean;
  signal?: AbortSignal;
  intervalMs?: number;
  generationTimeoutMs?: number;
}

export type Generate = (prompt: string, signal?: AbortSignal) => Promise<string>;

// The OpenCode Effect entrypoint returns an Effect operation, while older hosts
// return a Promise. Accept both, interrupt the Effect on abort, and normalize
// the text result.
export async function runGeneration(operation: () => unknown, signal?: AbortSignal): Promise<string> {
  if (signal?.aborted) throw new Error("generation aborted");
  const result = operation();
  let value: unknown;
  if (result && typeof (result as { pipe?: unknown }).pipe === "function") {
    const fiber = Effect.runFork(result as Effect.Effect<unknown>);
    value = await new Promise((resolve, reject) => {
      const onAbort = () => {
        void Effect.runPromise(Fiber.interrupt(fiber)).catch(() => {});
        reject(new Error("generation aborted"));
      };
      signal?.addEventListener("abort", onAbort, { once: true });
      Effect.runPromise(Fiber.join(fiber)).then(
        (completed) => { signal?.removeEventListener("abort", onAbort); resolve(completed); },
        (error) => { signal?.removeEventListener("abort", onAbort); reject(error); },
      );
    });
  } else {
    value = await result;
  }
  if (signal?.aborted) throw new Error("generation aborted");
  if (typeof value === "string") return value;
  return typeof (value as { text?: unknown })?.text === "string" ? (value as { text: string }).text : "";
}

interface EngineClient {
  jobsNext(params: unknown): Promise<{ job: any; lease: string }>;
  jobsComplete(params: unknown): Promise<any>;
  isDead?(): boolean;
}

const GENERATION_TIMEOUT_MS = 120_000;
const IDLE_INTERVAL_MS = 1_500;
const RETRY_BASE_MS = 1_000;
const RETRY_MAX_MS = 30_000;

// Stops waiting when an old host ignores the signal. Completion after
// cancellation must still reach the engine.
function raceAbort<T>(promise: Promise<T>, signal: AbortSignal): Promise<T> {
  if (signal.aborted) return Promise.reject(new Error("generation aborted"));
  return new Promise<T>((resolve, reject) => {
    const onAbort = () => reject(new Error("generation aborted"));
    signal.addEventListener("abort", onAbort, { once: true });
    promise.then(
      (value) => { signal.removeEventListener("abort", onAbort); resolve(value); },
      (error) => { signal.removeEventListener("abort", onAbort); reject(error); },
    );
  });
}

export async function runOneJob(
  client: EngineClient,
  generate: Generate | null,
  options: WorkerOptions = {},
): Promise<boolean> {
  if (options.signal?.aborted) return false;
  const generation = options.generation ?? true;
  const { job, lease } = await client.jobsNext({ generation });
  if (!job) return false;

  const complete = (fields: Record<string, unknown>) =>
    client.jobsComplete({ jobID: job.id, lease, ...fields });

  if (options.signal?.aborted) {
    await complete({ failure: "cancelled" });
    return false;
  }
  if (job.deterministic === true) {
    await complete({ output: "" });
    return true;
  }
  if (!generate) {
    await complete({ failure: "generation_unavailable" });
    return false;
  }

  const prompt = typeof job.prompt === "string" ? job.prompt : `Recollect ${job.kind} job ${job.id}`;
  const controller = new AbortController();
  const onParentAbort = () => controller.abort();
  options.signal?.addEventListener("abort", onParentAbort, { once: true });
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, options.generationTimeoutMs ?? GENERATION_TIMEOUT_MS);

  let output: string;
  try {
    output = await raceAbort(generate(prompt, controller.signal), controller.signal);
  } catch {
    if (timedOut) {
      await complete({ failure: "generation_timeout" });
      return false;
    }
    if (controller.signal.aborted || options.signal?.aborted) {
      await complete({ failure: "cancelled" });
      return false;
    }
    await complete({ failure: "generation_failed" });
    return false;
  } finally {
    clearTimeout(timer);
    options.signal?.removeEventListener("abort", onParentAbort);
  }

  if (options.signal?.aborted) {
    await complete({ failure: "cancelled" });
    return false;
  }

  await complete({ output });
  return true;
}

export function startWorker(
  client: EngineClient,
  generate: Generate | null,
  options: WorkerOptions = {},
): { stop(): Promise<void> } {
  let stopped = false;
  let retryDelayMs = 0;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let wake: (() => void) | undefined;
  const signal = options.signal;
  if (signal?.aborted) return { stop: () => Promise.resolve() };

  const wait = (ms: number) => {
    if (stopped) return Promise.resolve();
    return new Promise<void>((resolve) => {
      wake = resolve;
      timer = setTimeout(() => {
        timer = undefined;
        wake = undefined;
        resolve();
      }, ms);
    });
  };

  const onAbort = () => {
    stopped = true;
    if (timer !== undefined) clearTimeout(timer);
    timer = undefined;
    const resume = wake;
    wake = undefined;
    resume?.();
  };
  signal?.addEventListener("abort", onAbort, { once: true });

  const poll = async () => {
    while (!stopped && client.isDead?.() !== true) {
      let delayMs = options.intervalMs ?? IDLE_INTERVAL_MS;
      try {
        await runOneJob(client, generate, options);
        retryDelayMs = 0;
      } catch {
        if (client.isDead?.() === true) {
          process.stderr.write("recollect: engine transport closed; job polling stopped\n");
          break;
        }
        // A stale lease or timeout backs off before the next poll.
        retryDelayMs = retryDelayMs === 0 ? RETRY_BASE_MS : Math.min(retryDelayMs * 2, RETRY_MAX_MS);
        delayMs = retryDelayMs;
      }
      if (stopped) break;
      await wait(delayMs);
    }
  };
  const pollPromise = poll();

  return {
    async stop() {
      stopped = true;
      signal?.removeEventListener("abort", onAbort);
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      const resume = wake;
      wake = undefined;
      resume?.();
      await pollPromise;
    },
  };
}
