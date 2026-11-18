// audit: behavior - Polls jobs.next with generation=true and completes a leased generation job.
// audit: regression - Missing generation capability or a lost lease must not silently succeed.
// audit: coverage - Covers generation flag, prompt handoff, completion, and cancellation.
// audit: seam - Fake client and generate callback exercise the worker without an OpenCode host.

import { describe, expect, test } from "bun:test";
import { runOneJob } from "./worker";

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

  test("reports a cancelled generation as a failed completion", async () => {
    const client = fakeClient({ id: "job-2", kind: "digest", harnessID: "opencode", state: "leased", prompt: "P" });
    const controller = new AbortController();
    controller.abort();
    const generate = async () => "SHOULD_NOT_APPLY";

    const applied = await runOneJob(client as any, generate, { generation: true, signal: controller.signal });

    expect(applied).toBe(false);
    expect(client.completeCalls[0]).toMatchObject({ jobID: "job-2", lease: "lease-1", failure: "cancelled" });
  });
});
