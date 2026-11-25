// Per-workspace Recollect memory for DeepSeek Harness.
// Each agent gets a scoped tool and a memory snapshot bound to that agent's working directory.

import { createHash } from 'node:crypto';
import { realpathSync } from 'node:fs';
import { homedir } from 'node:os';
import { posix, win32 } from 'node:path';

export const TOOL_NAME = 'recollect_memory';
export const inject = ['tools', 'systemPrompt', 'workingDirectory', 'sessions', 'subprocess', 'agents'];

const RPC_TIMEOUT_MS = 5000;
const TOOL_TIMEOUT_MS = RPC_TIMEOUT_MS + 1000;
const STDERR_LIMIT = 2048;
const RESOLVE_TIMEOUT_MS = 5000;

const present = (value) => (typeof value === 'string' && value.trim() !== '' ? value : null);

function userRoot(platform, env) {
  if (platform === 'win32') {
    const local = present(env.LOCALAPPDATA)
      ?? (present(env.USERPROFILE) ? win32.join(env.USERPROFILE, 'AppData', 'Local') : null)
      ?? win32.join(homedir(), 'AppData', 'Local');
    return win32.join(local, 'recollect');
  }
  return posix.join(present(env.HOME) ?? homedir(), '.recollect');
}

function binaryPath(platform, env, configured) {
  if (present(configured)) return configured;
  if (present(env.RECOLLECT_BINARY)) return env.RECOLLECT_BINARY;
  if (platform === 'win32') {
    const local = present(env.LOCALAPPDATA)
      ?? (present(env.USERPROFILE) ? win32.join(env.USERPROFILE, 'AppData', 'Local') : null)
      ?? win32.join(homedir(), 'AppData', 'Local');
    return win32.join(local, 'Programs', 'recollect', 'recollect.exe');
  }
  return posix.join(present(env.HOME) ?? homedir(), '.local', 'bin', 'recollect');
}

export function resolveConfig(options = {}, host = {}) {
  const platform = host.platform ?? process.platform;
  const env = host.env ?? process.env;
  const rawUserDir = options.userDir ?? userRoot(platform, env);
  const userDir = typeof rawUserDir === 'string' ? rawUserDir.trim() : '';
  const absolute = platform === 'win32' ? win32.isAbsolute(userDir) : posix.isAbsolute(userDir);
  if (!userDir || !absolute) {
    throw new Error(`dsh-recollect: userDir must be an absolute path, got ${JSON.stringify(rawUserDir)}`);
  }
  return {
    binaryPath: binaryPath(platform, env, options.binaryPath),
    userDir,
    harnessID: 'dsh-workspace',
  };
}

// The input must already be canonical. Engine bindings own canonicalization;
// this function only turns the canonical root into this harness's store path.
export function projectDirFor(userDir, workspaceRoot, platform = process.platform) {
  const projectID = createHash('sha256').update(workspaceRoot).digest('hex').slice(0, 16);
  return (platform === 'win32' ? win32.join : posix.join)(userDir, 'projects', projectID);
}

function localCanonicalRoot(workspaceRoot, platform) {
  try {
    return realpathSync.native(workspaceRoot);
  } catch {
    return (platform === 'win32' ? win32.resolve : posix.resolve)(workspaceRoot);
  }
}

// Delegate canonicalization to the engine so every harness hashes the same
// root. A failed lookup falls back to the local filesystem's real path.
async function engineResolve(ctx, binary, workspaceRoot, signal) {
  const handle = ctx.subprocess.spawn({
    argv: [binary, 'resolve', '--root', workspaceRoot, '--json'],
    cwd: workspaceRoot,
    stdio: { stdin: 'ignore', stdout: 'pipe', stderr: 'pipe' },
    graceMs: RESOLVE_TIMEOUT_MS,
    signal,
  });
  let text = '';
  handle.stdout?.setEncoding('utf8');
  handle.stdout?.on('data', (chunk) => { text += chunk; });
  const ended = new Promise((resolve) => {
    if (!handle.stdout) resolve();
    else handle.stdout.once('end', resolve);
  });
  let timer;
  try {
    const [outcome] = await Promise.race([
      Promise.all([handle.done, ended]),
      new Promise((_, reject) => {
        timer = setTimeout(() => reject(new Error('resolve timed out')), RESOLVE_TIMEOUT_MS);
      }),
    ]);
    if (outcome?.exitCode !== 0) return null;
    const parsed = JSON.parse(text);
    if (typeof parsed?.projectRoot !== 'string' || !parsed.projectRoot) return null;
    return parsed;
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
    handle.terminate();
  }
}

function asyncRpc(handle, timeoutMs) {
  let buffer = '';
  let nextID = 1;
  let failure = null;
  const pending = new Map();

  const failAll = (error) => {
    if (failure) return;
    failure = error;
    for (const waiting of pending.values()) {
      waiting.cleanup();
      waiting.reject(error);
    }
    pending.clear();
  };

  handle.stdout.setEncoding('utf8');
  handle.stdout.on('data', (chunk) => {
    buffer += chunk;
    let at = buffer.indexOf('\n');
    while (at >= 0) {
      const line = buffer.slice(0, at);
      buffer = buffer.slice(at + 1);
      at = buffer.indexOf('\n');
      let reply;
      try {
        reply = JSON.parse(line);
      } catch {
        continue;
      }
      const waiting = pending.get(Number(reply.id));
      if (!waiting) continue;
      waiting.cleanup();
      if (reply.error) waiting.reject(new Error(reply.error.message ?? 'RPC error'));
      else waiting.resolve(reply.result);
    }
  });
  handle.stdout.on('error', failAll);
  handle.stdin?.on('error', failAll);
  handle.done.then(
    (outcome) => failAll(new Error(`engine exited (code ${outcome?.exitCode}, signal ${outcome?.signal})`)),
    failAll,
  );

  return {
    fail: failAll,
    request(method, params = {}, signal) {
      if (failure) return Promise.reject(failure);
      if (signal?.aborted) return Promise.reject(signal.reason ?? new Error(`${method} aborted`));
      const id = nextID++;
      const line = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
      return new Promise((resolve, reject) => {
        let timer;
        let onAbort;
        const cleanup = () => {
          clearTimeout(timer);
          pending.delete(id);
          if (onAbort) signal?.removeEventListener('abort', onAbort);
        };
        const settleResolve = (value) => { cleanup(); resolve(value); };
        const settleReject = (error) => { cleanup(); reject(error); };
        onAbort = () => settleReject(signal.reason ?? new Error(`${method} aborted`));
        timer = setTimeout(() => settleReject(new Error(`${method} timed out`)), timeoutMs);
        pending.set(id, { resolve: settleResolve, reject: settleReject, cleanup });
        signal?.addEventListener('abort', onAbort, { once: true });
        try {
          handle.stdin.write(line);
        } catch (error) {
          cleanup();
          reject(error);
        }
      });
    },
    close() {
      failAll(new Error('engine closed'));
      try {
        handle.stdin.end();
      } catch {
        // The pipe is already closed.
      }
      handle.terminate();
      return handle.waitForExit().catch(() => {});
    },
  };
}

async function resolveBinary(ctx, configured, signal) {
  try {
    return await ctx.subprocess.resolveExecutable(configured, process.env, signal);
  } catch {
    if (signal?.aborted) throw signal.reason ?? new Error('engine startup aborted');
    try {
      return await ctx.subprocess.resolveExecutable('recollect', process.env, signal);
    } catch (fallbackError) {
      if (signal?.aborted) throw signal.reason ?? new Error('engine startup aborted');
      throw new Error(`Recollect binary not found: ${configured} (also tried PATH)`, { cause: fallbackError });
    }
  }
}

async function startEngine(ctx, config, workspaceRoot, projectRoot, signal) {
  const projectDir = projectDirFor(config.userDir, projectRoot, process.platform);
  const binary = await resolveBinary(ctx, config.binaryPath, signal);
  const handle = ctx.subprocess.spawn({
    argv: [binary],
    cwd: workspaceRoot,
    stdio: { stdin: 'pipe', stdout: 'pipe', stderr: 'pipe' },
    graceMs: 5000,
    signal,
  });
  const client = asyncRpc(handle, RPC_TIMEOUT_MS);
  let stderrTail = '';
  handle.stderr?.setEncoding('utf8');
  handle.stderr?.on('data', (chunk) => {
    stderrTail = (stderrTail + chunk).slice(-STDERR_LIMIT);
  });
  handle.stderr?.on('error', (error) => {
    client.fail(new Error(`engine stderr failed: ${error.message}`));
  });

  try {
    const init = await client.request('initialize', {
      projectDir,
      projectRoot,
      userDir: config.userDir,
      harnessID: config.harnessID,
      capabilities: ['context'],
      generationState: 'disabled',
      protocol: { major: 1, minor: 1 },
    }, signal);
    if (!init?.protocol || init.protocol.major !== 1) {
      throw new Error('unexpected Recollect protocol version');
    }
    return { client, projectDir, projectRoot, stderr: () => stderrTail };
  } catch (error) {
    const detail = stderrTail ? `${error.message} (engine stderr: ${stderrTail})` : error.message;
    void client.close();
    throw new Error(detail);
  }
}

function toolDefinition(engine) {
  const textSchema = (description) => ({ type: 'string', description });
  return {
    name: TOOL_NAME,
    description: "Read, search, add, correct, or delete facts in this workspace's Recollect memory.",
    parameters: {
      type: 'object',
      properties: {
        target: { type: 'string', enum: ['project', 'user'], description: 'Memory scope. `user` is shared across workspaces.' },
        operation: {
          type: 'string',
          enum: ['read', 'search', 'append', 'correct', 'delete'],
          description: 'Memory operation.',
        },
        requestID: textSchema('Idempotency key for mutations.'),
        name: textSchema('Stable fact name.'),
        description: textSchema('Short fact description.'),
        body: textSchema('Fact body.'),
        reason: textSchema('Reason required for a user append.'),
        revision: textSchema('Revision from a preceding read for edits.'),
        query: textSchema('Search query.'),
        scene: textSchema('Optional search scene.'),
        limit: { type: 'integer', description: 'Optional search result limit.' },
      },
      required: ['target', 'operation'],
    },
    output: {
      schema: { type: 'object', properties: { content: { type: 'string' } } },
      render: (_args, value) => [{ type: 'text', text: String(value?.content ?? JSON.stringify(value)) }],
    },
    timeoutMs: TOOL_TIMEOUT_MS,
    execute: async (args, exec) => {
      const payload = args && typeof args === 'object' ? args : {};
      const result = await engine.client.request('memory.execute', payload, exec?.signal);
      return {
        content: typeof result?.content === 'string'
          ? result.content
          : JSON.stringify(result) ?? '',
      };
    },
  };
}

export function apply(ctx, config = {}) {
  const resolved = resolveConfig(config, {
    platform: process.platform,
    env: process.env,
  });

  // Each agent's registrations live on its own context and are also owned here,
  // so unloading this plugin or disposing the agent tears them down in order.
  const perAgent = new Map();
  let unloaded = false;
  const alive = (agent, signal) => !unloaded && !signal?.aborted && Boolean(ctx.agents.get(agent.id));
  const teardown = (id) => {
    const entry = perAgent.get(id);
    if (!entry) return;
    perAgent.delete(id);
    if (entry.onAbort) entry.signal?.removeEventListener('abort', entry.onAbort);
    entry.dispose();
  };

  ctx.on('agent/created', async ({ agent, signal }) => {
    teardown(agent.id);
    if (unloaded || signal?.aborted) return;

    const session = ctx.sessions.get(agent.id);
    const workspaceRoot = session
      ? (ctx.workingDirectory.get(session) || ctx.workingDirectory.defaultDirectory)
      : null;
    if (!workspaceRoot) {
      ctx.logger.warn('recollect-workspace: no session workspace; skipping memory setup');
      return;
    }

    // Register ownership before the first await so a plugin unload during
    // startup can still close any partially created engine.
    const entry = {
      signal,
      onAbort: null,
      engine: null,
      effectDisposer: null,
      dispose() {
        this.effectDisposer?.();
        void this.engine?.client.close();
      },
    };
    perAgent.set(agent.id, entry);
    const onAbort = () => teardown(agent.id);
    entry.onAbort = onAbort;
    signal?.addEventListener('abort', onAbort, { once: true });

    let engine;
    try {
      const binary = await resolveBinary(ctx, resolved.binaryPath, signal);
      if (!alive(agent, signal)) return;
      const resolvedRoot = await engineResolve(ctx, binary, workspaceRoot, signal);
      if (!alive(agent, signal)) return;
      const projectRoot = resolvedRoot?.projectRoot ?? localCanonicalRoot(workspaceRoot, process.platform);
      engine = await startEngine(ctx, resolved, workspaceRoot, projectRoot, signal);
    } catch (error) {
      ctx.logger.warn(`recollect-workspace: engine startup failed for ${workspaceRoot}`, error);
      teardown(agent.id);
      return;
    }
    // A plugin unload can tear down the entry while startEngine is in flight.
    // Attach the engine only if this entry is still the live owner; otherwise
    // close it here so no child escapes the unload.
    if (perAgent.get(agent.id) !== entry || !alive(agent, signal)) {
      void engine.client.close();
      return;
    }
    entry.engine = engine;
    if (!alive(agent, signal)) {
      teardown(agent.id);
      return;
    }

    let sectionText = '';
    try {
      const contextResult = await engine.client.request('context.get', {}, signal);
      if (typeof contextResult?.section === 'string') sectionText = contextResult.section;
    } catch (error) {
      ctx.logger.warn('recollect-workspace: context snapshot failed; registering tool without a snapshot', error);
    }
    if (!alive(agent, signal)) {
      teardown(agent.id);
      return;
    }

    const scope = agent.ctx;
    if (!scope) {
      ctx.logger.warn('recollect-workspace: agent has no scope; skipping');
      teardown(agent.id);
      return;
    }

    // One effect owns both registrations on the agent scope. Keep its disposer
    // here too, because unloading the plugin does not dispose agent.ctx.
    try {
      entry.effectDisposer = scope.effect(() => {
        const sectionDisposer = scope.systemPrompt.section({
          name: 'recollect-workspace-memory',
          order: 100,
          text: sectionText,
        });
        try {
          const toolDisposer = scope.tools.register(toolDefinition(engine));
          return () => {
            toolDisposer();
            sectionDisposer();
          };
        } catch (error) {
          sectionDisposer();
          throw error;
        }
      }, 'recollect-workspace.agent');
    } catch (error) {
      ctx.logger.warn('recollect-workspace: registration failed', error);
      teardown(agent.id);
      return;
    }

    if (signal?.aborted) teardown(agent.id);
  });

  ctx.on('agent/disposed', ({ agent }) => teardown(agent.id));
  ctx.effect(() => () => {
    unloaded = true;
    for (const id of [...perAgent.keys()]) teardown(id);
  }, 'recollect-workspace');
}
