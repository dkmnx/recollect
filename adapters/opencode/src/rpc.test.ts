// audit: behavior - Frames JSON-RPC requests, maps stable error codes, respawns a dead engine, replays initialize, bounds reconnects, and applies deadlines to replay, retry, and engine-close dispatches.
// audit: regression - A dead engine, a dispose race, failed or timed-out replays, a second transport failure, repeated initialize, and factory throws must not hang or respawn forever.
// audit: coverage - Covers fake-carrier exit, replay param fidelity, shared replay, replay recovery, reconnect cap, deadline, isDead states, engineClose skips, and one real short-lived process.
// audit: seam - A Carrier factory, fake timers, and node:child_process stand in for engine stdio.

import { describe, expect, jest, test } from "bun:test";
import { ErrorCode, RPC_DEADLINE_MS, RpcClient, RpcError, spawnCarrier, type Carrier } from "./rpc";

class FakeChildProcess implements Carrier {
  raw: string[] = [];
  frames: Record<string, any>[] = [];
  diagnostics: string[] = [];
  private frameListeners = new Set<(line: string) => void>();
  private diagListeners = new Set<(line: string) => void>();
  private errorListeners = new Set<(error: Error) => void>();
  private exitListeners = new Set<(error: Error) => void>();

  send(line: string): void {
    this.raw.push(line);
    this.frames.push(JSON.parse(line));
  }
  onFrame(listener: (line: string) => void): () => void {
    this.frameListeners.add(listener);
    return () => this.frameListeners.delete(listener);
  }
  onDiagnostic(listener: (line: string) => void): () => void {
    this.diagListeners.add(listener);
    return () => this.diagListeners.delete(listener);
  }
  onError(listener: (error: Error) => void): () => void {
    this.errorListeners.add(listener);
    return () => this.errorListeners.delete(listener);
  }
  onExit(listener: (error: Error) => void): () => void {
    this.exitListeners.add(listener);
    return () => this.exitListeners.delete(listener);
  }
  dispose(): void {}
  emit(line: string): void {
    for (const listener of this.frameListeners) listener(line);
  }
  emitDiagnostic(line: string): void {
    for (const listener of this.diagListeners) listener(line);
  }
  emitError(error: Error): void {
    for (const listener of this.errorListeners) listener(error);
  }
  emitExit(error: Error): void {
    for (const listener of this.exitListeners) listener(error);
  }
  reply(id: unknown, payload: Record<string, unknown>): void {
    this.emit(JSON.stringify({ jsonrpc: "2.0", id, ...payload }) + "\n");
  }
}

class DisposingCarrier extends FakeChildProcess {
  onAfterFrame?: (line: string) => void;

  emit(line: string): void {
    super.emit(line);
    this.onAfterFrame?.(line);
  }
}

function settleWithin<T>(promise: Promise<T>, ms: number): Promise<T | "timeout"> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => resolve("timeout"), ms);
    promise.then(
      (value) => { clearTimeout(timer); resolve(value); },
      (error) => { clearTimeout(timer); reject(error); },
    );
  });
}

describe("RpcClient framing", () => {
  test("writes one newline-terminated JSON-RPC frame with a numeric id", () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    void client.request("status.get", { scope: "project" }).catch(() => {});

    expect(child.raw).toHaveLength(1);
    expect(child.raw[0].endsWith("\n")).toBe(true);
    expect(child.frames[0].jsonrpc).toBe("2.0");
    expect(child.frames[0].method).toBe("status.get");
    expect(child.frames[0].params).toEqual({ scope: "project" });
    expect(typeof child.frames[0].id).toBe("number");
    client.dispose();
  });

  test("correlates out-of-order replies to the right request", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const first = client.request("a", { n: 1 });
    const second = client.request("b", { n: 2 });

    child.reply(child.frames[1].id, { result: { value: 2 } });
    child.reply(child.frames[0].id, { result: { value: 1 } });

    expect(await first).toEqual({ value: 1 });
    expect(await second).toEqual({ value: 2 });
    client.dispose();
  });

  test("exposes the engine stable codes and typed errors", () => {
    expect(ErrorCode.ReadRequired).toBe(-32002);
    expect(ErrorCode.Revision).toBe(-32003);
    expect(ErrorCode.Busy).toBe(-32004);
    expect(ErrorCode.Conflict).toBe(-32005);
    expect(ErrorCode.Storage).toBe(-32006);
    expect(ErrorCode.Transport).toBe(-32007);
    expect(ErrorCode.InvalidParams).toBe(-32602);
    expect(ErrorCode.Protocol).toBe(-32001);
  });

  test("maps engine error codes to RpcError with code and message", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const cases: Array<[number, string]> = [
      [ErrorCode.ReadRequired, "read_required"],
      [ErrorCode.Revision, "revision_conflict"],
      [ErrorCode.Busy, "busy"],
      [ErrorCode.Conflict, "conflict"],
      [ErrorCode.Storage, "storage_failure"],
      [ErrorCode.InvalidParams, "fact_not_found"],
      [ErrorCode.Protocol, "protocol major 1 required"],
    ];
    for (const [code, message] of cases) {
      const pending = client.request("memory.execute", {});
      child.reply(child.frames.at(-1)!.id, { error: { code, message } });
      await expect(pending).rejects.toMatchObject({ code, message });
      await expect(pending).rejects.toBeInstanceOf(RpcError);
    }
    client.dispose();
  });

  test("records child stderr diagnostics without failing requests", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    child.emitDiagnostic("engine bound project scope\n");
    expect(client.lastDiagnostic).toContain("engine bound project scope");
    client.dispose();
  });

  test("convenience methods emit the documented method names", () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const calls: Array<[() => Promise<unknown>, string]> = [
      [() => client.initialize({}), "initialize"],
      [() => client.memoryExecute({}), "memory.execute"],
      [() => client.contextGet({}), "context.get"],
      [() => client.eventsIngest({}), "events.ingest"],
      [() => client.jobsNext({}), "jobs.next"],
      [() => client.jobsComplete({}), "jobs.complete"],
      [() => client.statusGet({}), "status.get"],
      [() => client.engineClose({}), "engine.close"],
    ];
    for (const [call, method] of calls) {
      void call().catch(() => {});
      expect(child.frames.at(-1)!.method).toBe(method);
    }
    client.dispose();
  });
});

describe("RpcClient child death", () => {
  test("rejects a pending request when the child exits", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const pending = client.statusGet().then(() => null, (error) => error);
    child.emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const error = await settleWithin(pending, 1000);
    expect(error).toBeInstanceOf(RpcError);
    expect((error as RpcError).code).toBe(ErrorCode.Transport);
    expect((error as RpcError).message).toBe("engine process exited");
    client.dispose();
  });

  test("rejects every request immediately after a transport failure", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    child.emitError(new RpcError(ErrorCode.Transport, "engine process exited"));

    expect(child.frames).toHaveLength(0);
    const error = await settleWithin(client.statusGet().then(() => null, (failure) => failure), 1000);
    expect(error).toBeInstanceOf(RpcError);
    expect((error as RpcError).code).toBe(ErrorCode.Transport);
    expect(child.frames).toHaveLength(0);
    client.dispose();
  });

  test("respawns the engine and replays initialize before the next request", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames.at(-1)!.id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet();
    expect(carriers).toHaveLength(2);
    expect(carriers[1].frames[0].method).toBe("initialize");
    carriers[1].reply(carriers[1].frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize", "status.get"]);
    carriers[1].reply(carriers[1].frames[1].id, { result: { health: "ok" } });
    expect(await status).toEqual({ health: "ok" });
    client.dispose();
  });

  test("rejects a request when dispose races a completed replay", async () => {
    const carriers: DisposingCarrier[] = [];
    let client!: RpcClient;
    client = new RpcClient(() => {
      const carrier = new DisposingCarrier();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames.at(-1)!.id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet();
    carriers[1].onAfterFrame = () => client.dispose();
    carriers[1].reply(carriers[1].frames[0].id, { result: {} });
    const outcome = await settleWithin(status.then(() => null, (error) => error), 1000);

    expect(outcome).toBeInstanceOf(RpcError);
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize"]);
  });

  test("does not hang engineClose when initialize replay fails", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames.at(-1)!.id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet();
    const close = client.engineClose();
    carriers[1].reply(carriers[1].frames[0].id, {
      error: { code: ErrorCode.InvalidParams, message: "bad replay params" },
    });

    const statusOutcome = await settleWithin(status.then(() => null, (error) => error), 1000);
    const closeOutcome = await settleWithin(close.then((value) => value, (error) => error), 1000);
    expect(statusOutcome).toBeInstanceOf(RpcError);
    expect(closeOutcome).toBeInstanceOf(RpcError);
    expect((closeOutcome as RpcError).code).toBe(ErrorCode.InvalidParams);
    client.dispose();
  });

  test("rejects after a real short-lived child exits without hanging", async () => {
    const command = process.platform === "win32" ? "where.exe" : "true";
    const carrier = spawnCarrier(command);
    let exited = false;
    await settleWithin(
      new Promise<void>((resolve) => {
        carrier.onExit?.(() => { exited = true; resolve(); });
      }),
      2000,
    );

    const client = new RpcClient(carrier);
    const error = await settleWithin(client.statusGet().then(() => null, (failure) => failure), 1000);
    client.dispose();

    expect(exited).toBe(true);
    expect(error).toBeInstanceOf(RpcError);
    expect((error as RpcError).code).toBe(ErrorCode.Transport);
  });
});

describe("RpcClient recovery guards", () => {
  test("replays the original initialize params after respawn", async () => {
    const params = { projectDir: "/work/demo", options: { readOnly: true } };
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize(params);
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet();
    expect(carriers[1].frames[0].method).toBe("initialize");
    expect(carriers[1].frames[0].params).toEqual(params);
    carriers[1].reply(carriers[1].frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));
    carriers[1].reply(carriers[1].frames[1].id, { result: { health: "ok" } });
    expect(await status).toEqual({ health: "ok" });
    client.dispose();
  });

  test("two concurrent requests after one exit share one carrier and one replay", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const first = client.statusGet();
    const second = client.contextGet();
    expect(carriers).toHaveLength(2);
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize"]);

    carriers[1].reply(carriers[1].frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(carriers).toHaveLength(2);
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual([
      "initialize", "status.get", "context.get",
    ]);
    carriers[1].reply(carriers[1].frames[1].id, { result: { health: "ok" } });
    carriers[1].reply(carriers[1].frames[2].id, { result: { context: "ready" } });
    expect(await first).toEqual({ health: "ok" });
    expect(await second).toEqual({ context: "ready" });
    client.dispose();
  });

  test("a second transport failure rejects without dispatching on a third carrier", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet().then(() => null, (error) => error);
    carriers[1].emitError(new RpcError(ErrorCode.Transport, "second transport failure"));

    const outcome = await settleWithin(status, 1000);
    expect(outcome).toBeInstanceOf(RpcError);
    expect((outcome as RpcError).message).toBe("second transport failure");
    expect(carriers).toHaveLength(2);
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize"]);
    client.dispose();
  });

  test("a failed replay still allows a later successful respawn", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const failed = client.statusGet().then(() => null, (error) => error);
    carriers[1].reply(carriers[1].frames[0].id, {
      error: { code: ErrorCode.InvalidParams, message: "bad replay params" },
    });
    expect(await settleWithin(failed, 1000)).toBeInstanceOf(RpcError);
    expect(client.isDead()).toBe(false);

    const status = client.statusGet();
    const carrier = carriers[2];
    carrier.reply(carrier.frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(carrier.frames.map((frame) => frame.method)).toEqual(["initialize", "status.get"]);
    carrier.reply(carrier.frames[1].id, { result: { health: "ok" } });
    expect(await status).toEqual({ health: "ok" });
    expect(client.isDead()).toBe(false);
    client.dispose();
  });

  test("direct-carrier client stays alive on a transport error frame and dies on dispose", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const pending = client.statusGet().then(() => null, (error) => error);
    child.reply(child.frames[0].id, {
      error: { code: ErrorCode.Transport, message: "engine process exited" },
    });
    expect(await settleWithin(pending, 1000)).toBeInstanceOf(RpcError);
    expect(client.isDead()).toBe(false);
    client.dispose();
    expect(client.isDead()).toBe(true);
  });

  test("direct-carrier client is dead after a fatal transport failure", () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    expect(client.isDead()).toBe(false);
    child.emitError(new RpcError(ErrorCode.Transport, "engine process exited"));
    expect(client.isDead()).toBe(true);
  });

  test("factory-backed client is not dead after a transient failure", () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    expect(client.isDead()).toBe(false);
    carriers[0].emitError(new RpcError(ErrorCode.Transport, "engine process exited"));
    expect(client.isDead()).toBe(false);
    client.dispose();
    expect(client.isDead()).toBe(true);
  });

  test("three failed reconnects latch isDead and stop respawning", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    for (let attempt = 1; attempt <= 3; attempt++) {
      const pending = client.statusGet().then(() => null, (error) => error);
      const carrier = carriers[attempt];
      expect(carrier.frames.map((frame) => frame.method)).toEqual(["initialize"]);
      carrier.reply(carrier.frames[0].id, {
        error: { code: ErrorCode.InvalidParams, message: `replay ${attempt} failed` },
      });
      const outcome = await settleWithin(pending, 1000);
      expect(outcome).toBeInstanceOf(RpcError);
      expect((outcome as RpcError).message).toBe(`replay ${attempt} failed`);
      expect(client.isDead()).toBe(attempt === 3);
    }
    expect(carriers).toHaveLength(4);

    const after = client.statusGet().then(() => null, (error) => error);
    expect(await settleWithin(after, 1000)).toBeInstanceOf(RpcError);
    expect(carriers).toHaveLength(4);
    expect(client.isDead()).toBe(true);
    client.dispose();
  });

  test("initialize replay timeout rejects within the deadline", async () => {
    jest.useFakeTimers();
    try {
      const carriers: FakeChildProcess[] = [];
      const client = new RpcClient(() => {
        const carrier = new FakeChildProcess();
        carriers.push(carrier);
        return carrier;
      });
      const init = client.initialize({ projectDir: "/work/demo" });
      carriers[0].reply(carriers[0].frames[0].id, { result: {} });
      await init;
      carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

      const status = client.statusGet().then(() => null, (error) => error);
      jest.advanceTimersByTime(RPC_DEADLINE_MS);
      const outcome = await status;
      expect(outcome).toBeInstanceOf(RpcError);
      expect((outcome as RpcError).code).toBe(ErrorCode.Transport);
      expect((outcome as RpcError).message).toBe("initialize replay timed out");
      expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize"]);
      client.dispose();
    } finally {
      jest.useRealTimers();
    }
  });

  test("a second initialize is rejected without overwriting the original params", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    const first = client.initialize({ projectDir: "/first" });
    child.reply(child.frames[0].id, { result: {} });
    await first;

    const second = client.initialize({ projectDir: "/second" }).then(() => null, (error) => error);
    const outcome = await settleWithin(second, 1000);
    expect(outcome).toBeInstanceOf(RpcError);
    expect((outcome as RpcError).code).toBe(ErrorCode.InvalidRequest);
    expect((outcome as RpcError).message).toBe("already initialized");
    expect(child.frames).toHaveLength(1);
    expect(child.frames[0].params).toEqual({ projectDir: "/first" });
    client.dispose();
  });

  test("engineClose reports skipped disconnects", async () => {
    const child = new FakeChildProcess();
    const client = new RpcClient(child);
    child.emitError(new RpcError(ErrorCode.Transport, "engine process exited"));
    expect(await client.engineClose()).toEqual({ skipped: "transport" });
    client.dispose();
    expect(await client.engineClose()).toEqual({ skipped: "disposed" });
  });

  test("engineClose reports a skip when dispatch rejects", async () => {
    const child = new FakeChildProcess();
    child.send = () => { throw new RpcError(ErrorCode.Transport, "engine process exited"); };
    const client = new RpcClient(child);
    expect(await client.engineClose()).toEqual({ skipped: "transport" });
    client.dispose();
  });

  test("a factory throw during reconnect is counted and propagates safely", async () => {
    const carriers: FakeChildProcess[] = [];
    let calls = 0;
    const client = new RpcClient(() => {
      calls++;
      if (calls === 2) throw new Error("spawn failed");
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const failed = client.statusGet().then(() => null, (error) => error);
    const outcome = await settleWithin(failed, 1000);
    expect(outcome).toBeInstanceOf(Error);
    expect((outcome as Error).message).toBe("spawn failed");
    expect(client.isDead()).toBe(false);

    const status = client.statusGet();
    const carrier = carriers[1];
    carrier.reply(carrier.frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));
    carrier.reply(carrier.frames[1].id, { result: { health: "ok" } });
    expect(await status).toEqual({ health: "ok" });
    client.dispose();
  });

  test("a replay-success crash loop latches isDead and stops respawning", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      const send = carrier.send.bind(carrier);
      carrier.send = (line: string) => {
        send(line);
        const frame = JSON.parse(line);
        queueMicrotask(() => {
          if (frame.method === "initialize") {
            carrier.reply(frame.id, { result: {} });
          } else {
            carrier.emitError(new RpcError(ErrorCode.Transport, "crash on request"));
          }
        });
      };
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    expect(await settleWithin(init, 1000)).toEqual({});
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    for (let i = 0; i < 6; i++) {
      const pending = client.statusGet().then(() => null, (error) => error);
      expect(await settleWithin(pending, 1000)).toBeInstanceOf(RpcError);
    }
    expect(client.isDead()).toBe(true);
    const count = carriers.length;
    const after = client.statusGet().then(() => null, (error) => error);
    expect(await settleWithin(after, 1000)).toBeInstanceOf(RpcError);
    expect(carriers).toHaveLength(count);
    client.dispose();
  });

  test("engineClose settles after a non-transport replay failure without dispatching", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    const status = client.statusGet().then(() => null, (error) => error);
    carriers[1].reply(carriers[1].frames[0].id, {
      error: { code: ErrorCode.InvalidParams, message: "bad replay params" },
    });
    expect(await settleWithin(status, 1000)).toBeInstanceOf(RpcError);

    const close = client.engineClose().then((value) => value, (error) => error);
    const outcome = await settleWithin(close, 1000);
    expect(outcome).toBeInstanceOf(RpcError);
    expect((outcome as RpcError).code).toBe(ErrorCode.InvalidParams);
    expect(carriers[1].frames.map((frame) => frame.method)).toEqual(["initialize"]);
    client.dispose();
  });

  test("a concurrent initialize joins the in-flight call without overwriting params", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const first = client.initialize({ projectDir: "/first" });
    const second = client.initialize({ projectDir: "/second" });
    expect(second).toBe(first);
    expect(carriers[0].frames).toHaveLength(1);
    expect(carriers[0].frames[0].params).toEqual({ projectDir: "/first" });

    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    expect(await settleWithin(first, 1000)).toEqual({});
    expect(await settleWithin(second, 1000)).toEqual({});
    client.dispose();
  });

  test("a timed-out retry rejects within the deadline", async () => {
    jest.useFakeTimers();
    try {
      const carriers: FakeChildProcess[] = [];
      let calls = 0;
      const client = new RpcClient(() => {
        const carrier = new FakeChildProcess();
        if (calls === 0) {
          const send = carrier.send.bind(carrier);
          carrier.send = (line: string) => {
            send(line);
            if (JSON.parse(line).method !== "initialize") {
              throw new RpcError(ErrorCode.Transport, "first request crashed");
            }
          };
        }
        calls++;
        carriers.push(carrier);
        return carrier;
      });
      const init = client.initialize({ projectDir: "/work/demo" });
      carriers[0].reply(carriers[0].frames[0].id, { result: {} });
      await init;

      const status = client.statusGet().then(() => null, (error) => error);
      for (let i = 0; i < 4; i++) await Promise.resolve();
      carriers[1].reply(carriers[1].frames[0].id, { result: {} });
      for (let i = 0; i < 12; i++) await Promise.resolve();
      jest.advanceTimersByTime(RPC_DEADLINE_MS);
      const outcome = await status;
      expect(outcome).toBeInstanceOf(RpcError);
      expect((outcome as RpcError).code).toBe(ErrorCode.Transport);
      expect((outcome as RpcError).message).toBe("status.get retry timed out");
      client.dispose();
    } finally {
      jest.useRealTimers();
    }
  });

  test("a transport error frame does not orphan concurrent requests", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;

    const first = client.statusGet().then((value) => value, (error) => error);
    const second = client.contextGet().then((value) => value, (error) => error);
    carriers[0].reply(carriers[0].frames[1].id, {
      error: { code: ErrorCode.Transport, message: "transport frame" },
    });
    await new Promise((resolve) => setTimeout(resolve, 0));
    carriers[1].reply(carriers[1].frames[0].id, { result: {} });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(carriers[1].frames.map((frame) => frame.method)).toEqual([
      "initialize", "status.get", "context.get",
    ]);
    carriers[1].reply(carriers[1].frames[1].id, { result: { health: "ok" } });
    carriers[1].reply(carriers[1].frames[2].id, { result: { context: "ready" } });
    expect(await settleWithin(first, 1000)).toEqual({ health: "ok" });
    expect(await settleWithin(second, 1000)).toEqual({ context: "ready" });
    client.dispose();
  });

  test("engineClose rethrows a latched application error after the cap lapses", async () => {
    const carriers: FakeChildProcess[] = [];
    const client = new RpcClient(() => {
      const carrier = new FakeChildProcess();
      carriers.push(carrier);
      return carrier;
    });
    const init = client.initialize({ projectDir: "/work/demo" });
    carriers[0].reply(carriers[0].frames[0].id, { result: {} });
    await init;
    carriers[0].emitExit(new RpcError(ErrorCode.Transport, "engine process exited"));

    for (let attempt = 1; attempt <= 3; attempt++) {
      const pending = client.statusGet().then(() => null, (error) => error);
      carriers[attempt].reply(carriers[attempt].frames[0].id, {
        error: { code: ErrorCode.InvalidParams, message: `replay ${attempt} failed` },
      });
      await settleWithin(pending, 1000);
    }
    expect(client.isDead()).toBe(true);

    const close = client.engineClose().then((value) => value, (error) => error);
    const outcome = await settleWithin(close, 1000);
    expect(outcome).toBeInstanceOf(RpcError);
    expect((outcome as RpcError).code).toBe(ErrorCode.InvalidParams);
    client.dispose();
  });
});
