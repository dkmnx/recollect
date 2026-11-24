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

export const RPC_DEADLINE_MS = 5000;
const MAX_RECONNECT_ATTEMPTS = 3;

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

function isTransportError(error: unknown): boolean {
  return error instanceof RpcError && error.code === ErrorCode.Transport;
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
  private readonly factory: (() => Carrier) | null;
  private carrier: Carrier;
  private initParams: unknown = null;
  private initialized = false;
  private initializing: Promise<any> | null = null;
  private generation = 0;
  private disposed = false;
  private reconnecting: Promise<void> | null = null;
  private reconnectAttempts = 0;
  private unrecoverable = false;
  lastDiagnostic = "";

  constructor(carrierOrFactory: Carrier | (() => Carrier)) {
    if (typeof carrierOrFactory === "function") {
      this.factory = carrierOrFactory;
      this.carrier = carrierOrFactory();
    } else {
      this.factory = null;
      this.carrier = carrierOrFactory;
    }
    this.attach();
  }

  private attach(): void {
    const generation = ++this.generation;
    const current = () => this.generation === generation;
    const carrier = this.carrier;
    this.unsubscribes.push(carrier.onFrame((line) => {
      if (current()) this.handleFrame(line);
    }));
    if (carrier.onDiagnostic) {
      this.unsubscribes.push(carrier.onDiagnostic((line) => {
        if (current()) this.lastDiagnostic = line.trim();
      }));
    }
    if (carrier.onError) {
      this.unsubscribes.push(carrier.onError((error) => {
        if (current()) this.failAll(error);
      }));
    }
    if (carrier.onExit) {
      this.unsubscribes.push(carrier.onExit((error) => {
        if (current()) this.failAll(error);
      }));
    }
  }

  private detach(): void {
    this.generation++;
    for (const off of this.unsubscribes) off();
    this.unsubscribes.length = 0;
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
    if (!this.failure) this.failure = error;
    for (const waiting of this.pending.values()) waiting.reject(this.failure);
    this.pending.clear();
  }

  private latchUnrecoverable(error: Error): void {
    this.unrecoverable = true;
    this.failAll(error);
  }

  private withDeadline<T>(promise: Promise<T>, operation: string): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        const error = new RpcError(ErrorCode.Transport, `${operation} timed out`);
        this.failAll(error);
        reject(error);
      }, RPC_DEADLINE_MS);
      promise.then(
        (value) => { clearTimeout(timer); resolve(value); },
        (cause) => { clearTimeout(timer); reject(cause); },
      );
    });
  }

  private dispatch(method: string, params: unknown): Promise<any> {
    if (this.disposed) {
      return Promise.reject(this.failure ?? new RpcError(ErrorCode.InvalidRequest, "client disposed"));
    }
    if (this.failure) return Promise.reject(this.failure);
    const id = this.nextID++;
    const frame = JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n";
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      try {
        this.carrier.send(frame);
      } catch (cause) {
        this.pending.delete(id);
        const error = cause instanceof Error ? cause : new Error(String(cause));
        this.failAll(error);
        reject(error);
      }
    });
  }

  private async performReconnect(factory: () => Carrier): Promise<void> {
    this.failAll(this.failure ?? new RpcError(ErrorCode.Transport, "engine reconnecting"));
    this.detach();
    this.carrier.dispose();
    this.lastDiagnostic = "";
    this.carrier = factory();
    this.failure = null;
    this.attach();
    try {
      if (this.failure) throw this.failure;
      if (this.initialized) {
        await this.withDeadline(this.dispatch("initialize", this.initParams), "initialize replay");
      }
    } catch (cause) {
      this.detach();
      this.carrier.dispose();
      throw cause;
    }
  }

  private reconnect(): Promise<void> {
    if (this.disposed) {
      return Promise.reject(this.failure ?? new RpcError(ErrorCode.InvalidRequest, "client disposed"));
    }
    if (this.unrecoverable) {
      return Promise.reject(this.failure ?? new RpcError(ErrorCode.Transport, "engine transport failed"));
    }
    if (this.reconnecting) return this.reconnecting;
    const factory = this.factory;
    if (!factory) {
      const error = this.failure ?? new RpcError(ErrorCode.Transport, "engine transport failed");
      this.latchUnrecoverable(error);
      return Promise.reject(error);
    }
    if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      const error = this.failure ?? new RpcError(ErrorCode.Transport, "engine transport failed");
      this.latchUnrecoverable(error);
      return Promise.reject(error);
    }
    this.reconnectAttempts++;
    let attempt: Promise<void>;
    attempt = this.performReconnect(factory).then(
      () => undefined,
      (cause) => {
        const error = cause instanceof Error ? cause : new Error(String(cause));
        this.failAll(error);
        if (this.reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) this.latchUnrecoverable(error);
        throw error;
      },
    ).finally(() => {
      if (this.reconnecting === attempt) this.reconnecting = null;
    });
    this.reconnecting = attempt;
    return attempt;
  }

  isDead(): boolean {
    return this.disposed || this.unrecoverable || (this.factory === null && this.failure !== null);
  }

  async request(method: string, params: unknown = {}): Promise<any> {
    if (this.disposed) {
      return Promise.reject(this.failure ?? new RpcError(ErrorCode.InvalidRequest, "client disposed"));
    }
    if (this.failure || this.reconnecting) await this.reconnect();
    let result: any;
    try {
      result = await this.dispatch(method, params);
    } catch (cause) {
      const error = cause instanceof Error ? cause : new Error(String(cause));
      if (!this.disposed && this.factory && isTransportError(error)) {
        await this.reconnect();
        result = await this.withDeadline(this.dispatch(method, params), `${method} retry`);
      } else {
        throw error;
      }
    }
    // shortcut: budget counts consecutive failures; a flapping engine may respawn once per successful request.
    // Upgrade to a time-window cap if flapping is observed in the field.
    this.reconnectAttempts = 0;
    return result;
  }

  initialize(params: unknown): Promise<any> {
    if (this.initialized) {
      return Promise.reject(new RpcError(ErrorCode.InvalidRequest, "already initialized"));
    }
    if (this.initializing) return this.initializing;
    this.initParams = params;
    this.initializing = this.request("initialize", params)
      .then((result) => {
        this.initialized = true;
        return result;
      })
      .finally(() => {
        this.initializing = null;
      });
    return this.initializing;
  }
  memoryExecute(params: unknown): Promise<any> { return this.request("memory.execute", params); }
  contextGet(params: unknown): Promise<any> { return this.request("context.get", params); }
  eventsIngest(params: unknown): Promise<any> { return this.request("events.ingest", params); }
  jobsNext(params: unknown): Promise<any> { return this.request("jobs.next", params); }
  jobsComplete(params: unknown): Promise<any> { return this.request("jobs.complete", params); }
  statusGet(params: unknown = {}): Promise<any> { return this.request("status.get", params); }

  async engineClose(params: unknown = {}): Promise<any> {
    if (this.disposed) return { skipped: "disposed" };
    if (this.failure) {
      if (isTransportError(this.failure)) return { skipped: "transport" };
      throw this.failure;
    }
    if (this.unrecoverable) return { skipped: "transport" };
    if (this.reconnecting) await this.reconnecting.catch(() => undefined);
    if (this.disposed) return { skipped: "disposed" };
    if (this.failure) {
      if (isTransportError(this.failure)) return { skipped: "transport" };
      throw this.failure;
    }
    if (this.unrecoverable) return { skipped: "transport" };
    try {
      return await this.withDeadline(this.dispatch("engine.close", params), "engine.close");
    } catch (cause) {
      if (isTransportError(cause)) return { skipped: "transport" };
      throw cause;
    }
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.detach();
    this.failAll(new RpcError(ErrorCode.InvalidRequest, "client disposed"));
    this.carrier.dispose();
  }
}

export function spawnCarrier(binaryPath: string): Carrier {
  const child = spawn(binaryPath, [], { stdio: ["pipe", "pipe", "pipe"], windowsHide: true });
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
  return new RpcClient(() => spawnCarrier(binaryPath));
}
