// Leases engine jobs and runs host generation without a conversation turn.

export interface WorkerOptions {
  generation?: boolean;
  signal?: AbortSignal;
  intervalMs?: number;
}

export type Generate = (prompt: string) => Promise<string>;

interface EngineClient {
  jobsNext(params: unknown): Promise<{ job: any; lease: string }>;
  jobsComplete(params: unknown): Promise<any>;
}

export async function runOneJob(
  client: EngineClient,
  generate: Generate | null,
  options: WorkerOptions = {},
): Promise<boolean> {
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
  let output: string;
  try {
    output = await generate(prompt);
  } catch {
    await complete({ failure: "generation_failed" });
    return false;
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
): { stop(): void } {
  let stopped = false;
  const signal = options.signal;
  const onAbort = () => { stopped = true; };
  signal?.addEventListener("abort", onAbort);

  const poll = async () => {
    while (!stopped) {
      try {
        await runOneJob(client, generate, options);
      } catch {
        // A stale lease or transport error is retried on the next poll.
      }
      await new Promise((resolve) => setTimeout(resolve, options.intervalMs ?? 1500));
    }
  };
  void poll();

  return {
    stop() {
      stopped = true;
      signal?.removeEventListener("abort", onAbort);
    },
  };
}
