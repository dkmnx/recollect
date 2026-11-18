// audit: behavior - Polls jobs.next, passes an abort signal into generation, interrupts Effect work, normalizes Effect or Promise output, stops on a dead transport, and reports cancelled or timed-out jobs.
// audit: regression - A missing capability, lost lease, abort, timeout, or dead engine must not apply late output or spin.
// audit: coverage - Covers generation flag, prompt handoff, completion, abort, timeout, dead-client stop, idle stop, and stop-time completion ordering; not backoff timing.
// audit: seam - Fake client and generate callback exercise the worker without an OpenCode host.

import { describe, expect, test } from "bun:test";
import { Effect } from "effect";
import { runGeneration, runOneJob, startWorker } from "./worker";

function settleWithin<T>(promise: Promise<T>, ms: number): Promise<T | "timeout"> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => resolve("timeout"), ms);
    promise.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
  });
}

function fakeClient(job: any) {
  const nextCalls: any[] = [];
  const completeCalls: any[] = [];
  return {
    nextCalls,
    completeCalls,
    jobsNext: async (params: any) => { nextCalls.push(params); return { job, lease: "lease-1" }; },
    jobsComplete: async (params: any) => { completeCalls.push(params); return { applied: true }; },
  };
}

describe("worker", () => {
  test("passes generation=true and completes a leased job with generated output", async () => {
    const client = fakeClient({ id: "job-1", kind: "fact", harnessID: "opencode", state: "leased", prompt: "Summarize" });
    const prompts: string[] = [];
    const generate = async (prompt: string) => { prompts.push(prompt); return "GENERATED"; };

    const applied = await runOneJob(client as any, generate, { generation: true });

    expect(client.nextCalls).toEqual([{ generation: true }]);
    expect(prompts).toEqual(["Summarize"]);
    expect(client.completeCalls).toEqual([{ jobID: "job-1", lease: "lease-1", output: "GENERATED" }]);
    expect(applied).toBe(true);
  });

  test("uses generation=false when no model capability is supplied", async () => {
    const client = fakeClient(null);
    const applied = await runOneJob(client as any, null, { generation: false });
    expect(client.nextCalls).toEqual([{ generation: false }]);
    expect(client.completeCalls).toHaveLength(0);
    expect(applied).toBe(false);
  });

  test("applies deterministic jobs without a model call", async () => {
    const client = fakeClient({ id: "job-3", kind: "revision", deterministic: true, harnessID: "opencode", state: "leased" });
    const applied = await runOneJob(client as any, null, { generation: true });
    expect(client.completeCalls).toEqual([{ jobID: "job-3", lease: "lease-1", output: "" }]);
    expect(applied).toBe(true);
  });

  test("does not lease when the signal is already aborted", async () => {
    const client = fakeClient({ id: "job-2", kind: "digest", harnessID: "opencode", state: "leased", prompt: "P" });
    const controller = new AbortController();
    controller.abort();
    const generate = async () => "SHOULD_NOT_APPLY";

    const applied = await runOneJob(client as any, generate, { generation: true, signal: controller.signal });

    expect(applied).toBe(false);
    expect(client.nextCalls).toHaveLength(0);
    expect(client.completeCalls).toHaveLength(0);
  });

  test("abandons a pending generation on abort and never applies its output", async () => {
    const client = fakeClient({ id: "job-4", kind: "digest", harnessID: "opencode", state: "leased", prompt: "P" });
    const controller = new AbortController();
    let started!: () => void;
    const startedPromise = new Promise<void>((resolve) => { started = resolve; });
    const generate = () => { started(); return new Promise<string>(() => {}); };

    const run = runOneJob(client as any, generate, { generation: true, signal: controller.signal });
    await startedPromise;
    controller.abort();

    expect(await settleWithin(run, 1000)).toBe(false);
    expect(client.completeCalls).toEqual([{ jobID: "job-4", lease: "lease-1", failure: "cancelled" }]);
  });

  test("fails a generation that exceeds its timeout and never applies its output", async () => {
    const client = fakeClient({ id: "job-5", kind: "digest", harnessID: "opencode", state: "leased", prompt: "P" });
    const generate = () => new Promise<string>(() => {});

    const run = runOneJob(client as any, generate, { generation: true, generationTimeoutMs: 10 });

    expect(await settleWithin(run, 1000)).toBe(false);
    expect(client.completeCalls).toEqual([{ jobID: "job-5", lease: "lease-1", failure: "generation_timeout" }]);
  });

  test("runs an Effect generation and returns its text", async () => {
    expect(await runGeneration(() => Effect.succeed({ text: "EFFECT" }))).toBe("EFFECT");
  });

  test("accepts a Promise generation from an older host", async () => {
    expect(await runGeneration(() => Promise.resolve("PLAIN"))).toBe("PLAIN");
  });

  test("interrupts an Effect generation when aborted", async () => {
    const controller = new AbortController();
    const pending = runGeneration(() => Effect.never, controller.signal);
    setTimeout(() => controller.abort(), 10);
    await expect(pending).rejects.toThrow("aborted");
  });

  test("passes an abort signal into generation", async () => {
    const client = fakeClient({ id: "job-6", kind: "fact", harnessID: "opencode", state: "leased", prompt: "P" });
    let seen: AbortSignal | undefined;
    const generate = async (_prompt: string, signal?: AbortSignal) => { seen = signal; return "X"; };
    const applied = await runOneJob(client as any, generate, { generation: true });
    expect(applied).toBe(true);
    expect(seen).toBeInstanceOf(AbortSignal);
  });

  test("stops polling when the client transport is dead", async () => {
    let calls = 0;
    let dead = false;
    const client = {
      jobsNext: async () => { calls += 1; dead = true; throw new Error("engine exited"); },
      jobsComplete: async () => ({}),
      isDead: () => dead,
    };
    const worker = startWorker(client as any, null, { intervalMs: 10 });
    await new Promise((resolve) => setTimeout(resolve, 50));
    worker.stop();
    expect(calls).toBe(1);
  });

  test("stop waits for the in-flight cancellation completion", async () => {
    const client = fakeClient({ id: "job-8", kind: "fact", harnessID: "opencode", state: "leased", prompt: "P" });
    const controller = new AbortController();
    let started!: () => void;
    const startedPromise = new Promise<void>((resolve) => { started = resolve; });
    const generate = () => { started(); return new Promise<string>(() => {}); };
    const worker = startWorker(client as any, generate, { signal: controller.signal, intervalMs: 60_000 });

    await startedPromise;
    const stopped = worker.stop();
    controller.abort();
    await stopped;

    expect(client.completeCalls.at(-1)).toMatchObject({ jobID: "job-8", lease: "lease-1", failure: "cancelled" });
  });

  test("stop releases an idle wait without another poll", async () => {
    let calls = 0;
    let started!: () => void;
    const first = new Promise<void>((resolve) => { started = resolve; });
    const client = {
      jobsNext: async () => { calls += 1; started(); return { job: null, lease: "" }; },
      jobsComplete: async () => ({}),
      isDead: () => false,
    };
    const worker = startWorker(client as any, null, { intervalMs: 60_000 });
    await first;
    worker.stop();
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(calls).toBe(1);
  });
});
