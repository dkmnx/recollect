// audit: behavior - Frames JSON-RPC requests, maps stable error codes, and fails requests when the child dies.
// audit: regression - Out-of-order replies and a dead engine must not resolve or hang requests.
// audit: coverage - Covers fake-carrier exit events and one real short-lived process, not a full engine.
// audit: seam - The Carrier boundary and node:child_process stand in for engine stdio.

import { describe, expect, test } from "bun:test";
import { ErrorCode, RpcClient, RpcError, spawnCarrier, type Carrier } from "./rpc";

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
