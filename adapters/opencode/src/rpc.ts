// JSON-RPC 2.0 client over the engine child process stdio.

import { spawn } from "node:child_process";

export const ErrorCode = {
  InvalidRequest: -32600,
  UnknownMethod: -32601,
  InvalidParams: -32602,
  Protocol: -32001,
  ReadRequired: -32002,
  Revision: -32003,
  Busy: -32004,
  Conflict: -32005,
  Storage: -32006,
  Transport: -32007,
} as const;

export class RpcError extends Error {
  constructor(
    readonly code: number,
    message: string,
    readonly data?: unknown,
  ) {
    super(message);
    this.name = "RpcError";
  }
}

export interface Carrier {
  send(line: string): void;
  onFrame(listener: (line: string) => void): () => void;
  onDiagnostic?(listener: (line: string) => void): () => void;
  onError?(listener: (error: Error) => void): () => void;
  onExit?(listener: (error: Error) => void): () => void;
  dispose(): void;
}

type Pending = { resolve: (value: any) => void; reject: (error: Error) => void };

export class RpcClient {
  private nextID = 1;
  private readonly pending = new Map<number, Pending>();
  private readonly unsubscribes: Array<() => void> = [];
  private failure: Error | null = null;
  lastDiagnostic = "";

  constructor(private readonly carrier: Carrier) {
    this.unsubscribes.push(carrier.onFrame((line) => this.handleFrame(line)));
    if (carrier.onDiagnostic) {
      this.unsubscribes.push(carrier.onDiagnostic((line) => {
        this.lastDiagnostic = line.trim();
      }));
    }
    if (carrier.onError) {
      this.unsubscribes.push(carrier.onError((error) => this.failAll(error)));
    }
    if (carrier.onExit) {
      this.unsubscribes.push(carrier.onExit((error) => this.failAll(error)));
    }
  }

  private handleFrame(line: string): void {
    const text = line.trim();
    if (!text) return;
    let response: any;
    try {
      response = JSON.parse(text);
    } catch {
      return;
    }
    const id = Number(response.id);
    const waiting = this.pending.get(id);
    if (!waiting) return;
    this.pending.delete(id);
    if (response.error) {
      const { code, message, data } = response.error;
      waiting.reject(new RpcError(code, message, data));
      return;
    }
    waiting.resolve(response.result);
  }

  private failAll(error: Error): void {
    this.failure = error;
    for (const waiting of this.pending.values()) waiting.reject(error);
    this.pending.clear();
  }

  isDead(): boolean {
    return this.failure !== null;
  }

  request(method: string, params: unknown = {}): Promise<any> {
    if (this.failure) return Promise.reject(this.failure);

    const id = this.nextID++;
    const frame = JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n";
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try {
        this.carrier.send(frame);
      } catch (error) {
        this.pending.delete(id);
        reject(error instanceof Error ? error : new Error(String(error)));
      }
    });
  }

  initialize(params: unknown): Promise<any> { return this.request("initialize", params); }
  memoryExecute(params: unknown): Promise<any> { return this.request("memory.execute", params); }
  contextGet(params: unknown): Promise<any> { return this.request("context.get", params); }
  eventsIngest(params: unknown): Promise<any> { return this.request("events.ingest", params); }
  jobsNext(params: unknown): Promise<any> { return this.request("jobs.next", params); }
  jobsComplete(params: unknown): Promise<any> { return this.request("jobs.complete", params); }
  statusGet(params: unknown = {}): Promise<any> { return this.request("status.get", params); }
  engineClose(params: unknown = {}): Promise<any> { return this.request("engine.close", params); }

  dispose(): void {
    for (const off of this.unsubscribes) off();
    this.unsubscribes.length = 0;
    this.failAll(new RpcError(ErrorCode.InvalidRequest, "client disposed"));
    this.carrier.dispose();
  }
}

export function spawnCarrier(binaryPath: string): Carrier {
  const child = spawn(binaryPath, [], { stdio: ["pipe", "pipe", "pipe"] });
  const frameListeners = new Set<(line: string) => void>();
  const diagListeners = new Set<(line: string) => void>();
  const errorListeners = new Set<(error: Error) => void>();
  const exitListeners = new Set<(error: Error) => void>();
  let stdoutBuffer = "";
  let transportFailure: Error | null = null;

  const notifyFailure = (error: Error) => {
    if (transportFailure) return;
    transportFailure = error;
    for (const listener of errorListeners) listener(error);
    for (const listener of exitListeners) listener(error);
  };

  child.stdout.setEncoding("utf8");
  child.stdout.on("data", (chunk: string) => {
    stdoutBuffer += chunk;
    let index = stdoutBuffer.indexOf("\n");
    while (index >= 0) {
      const line = stdoutBuffer.slice(0, index);
      stdoutBuffer = stdoutBuffer.slice(index + 1);
      for (const listener of frameListeners) listener(line);
      index = stdoutBuffer.indexOf("\n");
    }
  });

  child.stdout.on("error", (error: Error) => {
    notifyFailure(new RpcError(ErrorCode.Transport, `engine stdout failed: ${error.message}`));
  });

  child.stderr.setEncoding("utf8");
  child.stderr.on("data", (chunk: string) => {
    for (const listener of diagListeners) listener(chunk);
    process.stderr.write(chunk);
  });
  child.stderr.on("error", () => {
    // Diagnostics are best effort; a broken stderr pipe does not stop the protocol.
  });

  child.on("error", (error: Error) => {
    notifyFailure(new RpcError(ErrorCode.Transport, `engine process failed: ${error.message}`));
  });

  // close fires after the stdio pipes drain, so a final reply is not lost.
  child.on("close", () => {
    notifyFailure(new RpcError(ErrorCode.Transport, "engine process exited"));
  });

  child.stdin.on("error", (error: NodeJS.ErrnoException) => {
    const message = error.code === "EPIPE"
      ? "engine process stdin closed (EPIPE)"
      : "engine process stdin failed";
    notifyFailure(new RpcError(ErrorCode.Transport, message));
  });

  return {
    send(line) {
      if (transportFailure) throw transportFailure;
      if (child.exitCode !== null || !child.stdin.writable) {
        throw new RpcError(ErrorCode.Transport, "engine process exited");
      }
      child.stdin.write(line);
    },
    onFrame(listener) { frameListeners.add(listener); return () => frameListeners.delete(listener); },
    onDiagnostic(listener) { diagListeners.add(listener); return () => diagListeners.delete(listener); },
    onError(listener) { errorListeners.add(listener); return () => errorListeners.delete(listener); },
    onExit(listener) {
      if (transportFailure) {
        listener(transportFailure);
        return () => {};
      }
      exitListeners.add(listener);
      return () => exitListeners.delete(listener);
    },
    dispose() { child.kill(); },
  };
}

export function createRpcClient(binaryPath: string): RpcClient {
  return new RpcClient(spawnCarrier(binaryPath));
}
