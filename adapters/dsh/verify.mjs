// E2E verifier for the dsh-recollect per-workspace Host plugin.
// Run with: node verify.mjs <adapter-directory>
// audit: behavior - proves the shipped bundle delegates canonicalization, drives a real engine RPC round trip, and rejects invalid memory.execute arguments before they reach the engine.
// audit: regression - a lost registration, a fixed binding, or a removed argument guard would change the asserted schema, project id, or error result.
// audit: coverage gap - live DSH activation is verified separately with cordis_inspect and a real scoped tool call.
// audit: seam - the engine child is driven over its real stdio JSON-RPC boundary; the toolDefinition export lets the verifier call the registered tool without a live DSH host.

import { createHash } from 'node:crypto';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { spawn } from 'node:child_process';
import { join, dirname } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const bundle = process.argv[2] || here;
const repoRoot = join(bundle, '..', '..');
const read = (name) => readFileSync(join(bundle, name), 'utf8');
const sha = (file) => createHash('sha256').update(readFileSync(file)).digest('hex');

const checks = [];
function check(name, fn) {
  checks.push([name, fn]);
}

check('package manifest exposes a Host-only bundle', () => {
  const p = JSON.parse(read('package.json'));
  if (p.name !== 'dsh-recollect') throw new Error('unexpected bundle name');
  if (p.type !== 'module') throw new Error('type module required');
  if (p.license !== 'MIT') throw new Error('MIT license required');
  if (p.exports['.'] !== './index.js') throw new Error('Host entry missing');
  if (p.exports['./locale/*.json'] !== './locale/*.json') throw new Error('locale export missing');
  if (p.exports['./icon'] !== './icon.svg') throw new Error('icon export missing');
  if (p.dsh?.bundle?.patch !== './cordis.patch.yml') throw new Error('bundle patch missing');
  if (p.engines?.dsh !== '^0.2.3-alpha.2') throw new Error('engines.dsh mismatch');
  for (const file of ['index.js', 'cordis.patch.yml', 'locale/en.json', 'icon.svg', 'LICENSE']) {
    if (!p.files.includes(file)) throw new Error(`${file} not in files`);
  }
});

check('patch inserts one Host plugin row', () => {
  const text = read('cordis.patch.yml');
  if (!text.includes('id: recollect-workspace')) throw new Error('row id missing');
  if (!text.includes("name: 'dsh-recollect'")) throw new Error('row name missing');
  if (text.includes('dsh.client')) throw new Error('unexpected Client half');
});

check('locale carries display metadata', () => {
  const meta = JSON.parse(read('locale/en.json')).meta;
  if (!meta?.title || !meta?.description) throw new Error('meta.title/description required');
  if (!/workspace/i.test(meta.description)) throw new Error('description must state workspace behavior');
});

check('icon and license match the Recollect source', () => {
  if (sha(join(bundle, 'icon.svg')) !== sha(join(repoRoot, 'assets', 'icon.svg'))) throw new Error('icon differs');
  if (sha(join(bundle, 'icon.png')) !== sha(join(repoRoot, 'assets', 'icon.png'))) throw new Error('raster icon differs');
  if (sha(join(bundle, 'LICENSE')) !== sha(join(repoRoot, 'LICENSE'))) throw new Error('license differs');
});

const mod = await import(pathToFileURL(join(bundle, 'index.js')).href).catch((error) => {
  throw new Error(`index.js is not loadable: ${error.message}`);
});

check('plugin declares required services and tool name', () => {
  for (const service of ['tools', 'systemPrompt', 'workingDirectory', 'sessions', 'subprocess', 'agents']) {
    if (!mod.inject.includes(service)) throw new Error(`inject missing ${service}`);
  }
  if (typeof mod.apply !== 'function') throw new Error('apply export required');
  if (mod.TOOL_NAME !== 'recollect_memory') throw new Error('tool name mismatch');
});

check('tool rejects invalid memory.execute arguments before RPC', async () => {
  const calls = [];
  const tool = mod.toolDefinition({
    client: {
      request: (method, params) => {
        calls.push([method, params]);
        return Promise.resolve({ content: 'ok' });
      },
    },
  });
  // Each case mirrors a rule the engine enforces in internal/rpc/memory.go and
  // internal/engine/mutations.go. The guard fails these early; the engine stays
  // authoritative for everything else (revision conflicts, bad limit, etc.).
  const invalid = [
    [{ target: 'bogus', operation: 'read' }, 'target must be project or user'],
    [{ target: 'project', operation: 'bogus' }, 'unsupported operation'],
    [{ target: 'project', operation: 'read' }, 'name is required for read'],
    [{ target: 'project', operation: 'search' }, 'query is required for search'],
    [{ target: 'project', operation: 'append', name: 'x', body: 'b' }, 'requestID is required for append'],
    [{ target: 'project', operation: 'append', requestID: 'r1', body: 'b' }, 'description is required for append'],
    [{ target: 'user', operation: 'append', requestID: 'r1', description: 'd' }, 'reason is required for append'],
    [{ target: 'project', operation: 'correct', requestID: 'r1', revision: 'v1' }, 'name is required for correct'],
    [{ target: 'project', operation: 'delete', requestID: 'r1', revision: 'v1' }, 'name is required for delete'],
    [{ target: 'project', operation: 'correct', requestID: 'r1' }, 'revision is required for correct'],
    [{ target: 'project', operation: 'delete', requestID: 'r1' }, 'revision is required for delete'],
    [{ target: 'project', operation: 'correct', revision: 'v1', name: 'x' }, 'requestID is required for correct'],
    [{ target: 'project', operation: 'delete', revision: 'v1', name: 'x' }, 'requestID is required for delete'],
    [{ target: 'project', operation: 'read', name: '   ' }, 'name is required for read'],
    [null, 'target must be project or user'],
    [[], 'target must be project or user'],
  ];
  for (const [args, message] of invalid) {
    let error = null;
    try { await tool.execute(args); } catch (caught) { error = caught; }
    if (!error || !error.message.includes(message)) {
      throw new Error(`invalid args not rejected: ${JSON.stringify(args)} (${error?.message})`);
    }
  }
  if (calls.length) throw new Error(`invalid args reached the engine: ${calls.length} RPC call(s)`);

  // Valid combinations for every operation and both scopes must pass unchanged.
  const valid = [
    { target: 'project', operation: 'read', name: 'x' },
    { target: 'user', operation: 'read' },
    { target: 'user', operation: 'read', name: 'x' },
    { target: 'project', operation: 'search', query: 'q' },
    { target: 'user', operation: 'search', query: 'q' },
    { target: 'project', operation: 'append', requestID: 'r1', description: 'd', body: 'b' },
    { target: 'user', operation: 'append', requestID: 'r1', description: 'd', reason: 'why' },
    { target: 'project', operation: 'correct', requestID: 'r1', revision: 'v1', name: 'x' },
    { target: 'user', operation: 'correct', requestID: 'r1', revision: 'v1' },
    { target: 'project', operation: 'delete', requestID: 'r1', revision: 'v1', name: 'x' },
    { target: 'user', operation: 'delete', requestID: 'r1', revision: 'v1' },
  ];
  for (const args of valid) {
    try { await tool.execute(args); } catch (error) {
      throw new Error(`valid args rejected: ${JSON.stringify(args)} (${error.message})`);
    }
  }
  if (calls.length !== valid.length) {
    throw new Error(`expected ${valid.length} RPC calls for valid args, got ${calls.length}`);
  }
  for (let i = 0; i < calls.length; i += 1) {
    const [method, params] = calls[i];
    if (method !== 'memory.execute') throw new Error(`unexpected RPC method: ${method}`);
    if (JSON.stringify(params) !== JSON.stringify(valid[i])) {
      throw new Error(`valid args mutated: ${JSON.stringify(params)}`);
    }
  }
});

check('tool surfaces read revision so edits can use it', async () => {
  const calls = [];
  const tool = mod.toolDefinition({
    client: {
      request: (method, params) => {
        calls.push([method, params]);
        if (params.operation === 'read') {
          return Promise.resolve({ content: '---\nname: x\n---\nbody', revision: 'rev-123' });
        }
        return Promise.resolve({ target: params.target, operation: params.operation, revision: 'rev-456' });
      },
    },
  });
  const read = await tool.execute({ target: 'project', operation: 'read', name: 'x' });
  if (!read.content.includes('revision: rev-123')) {
    throw new Error(`read revision missing from tool output: ${JSON.stringify(read)}`);
  }
  const append = await tool.execute({
    target: 'project', operation: 'append', requestID: 'r1', description: 'd', body: 'b',
  });
  if (!append.content.includes('"revision":"rev-456"')) {
    throw new Error(`mutation revision missing from tool output: ${JSON.stringify(append)}`);
  }
});

check('shipped docs carry release links and correct verify command', () => {
  const changelog = read('CHANGELOG.md');
  if (!changelog.includes('[v0.1.0]: https://github.com/dkmnx/recollect/compare/9ddfd25cb1f64ff34c071785c27100c5caa5ae71...dsh-recollect-v0.1.0')) {
    throw new Error('CHANGELOG comparison link missing');
  }
  const readme = read('README.md');
  if (!readme.includes('node adapters/dsh/verify.mjs adapters/dsh')) {
    throw new Error('README verify command is not runnable from the repo root');
  }
});

check('relative userDir is rejected and absolute userDir is preserved', () => {
  const host = { platform: 'win32', env: { LOCALAPPDATA: 'C:\\Users\\dev\\AppData\\Local' } };
  for (const value of ['memory', '  ', '']) {
    let rejected = false;
    try { mod.resolveConfig({ userDir: value }, host); } catch { rejected = true; }
    if (!rejected) throw new Error(`relative userDir accepted: ${JSON.stringify(value)}`);
  }
  const config = mod.resolveConfig({ userDir: ' C:\\memory ' }, host);
  if (config.userDir !== 'C:\\memory') throw new Error('absolute userDir was not trimmed');
});

check('canonical roots resolve to isolated stores', () => {
  const a = mod.projectDirFor('C:\\memory', 'C:\\repo\\alpha', 'win32');
  const b = mod.projectDirFor('C:\\memory', 'C:\\repo\\beta', 'win32');
  if (a === b) throw new Error('workspace roots collapsed to one store');
  if (mod.projectDirFor('C:\\memory', 'C:\\repo\\alpha', 'win32') !== a) throw new Error('binding is not stable');
  const p = mod.projectDirFor('/memory', '/repo/alpha', 'posix');
  if (mod.projectDirFor('/memory', '/repo/beta', 'posix') === p) throw new Error('posix roots collapsed to one store');
});

const tempUserDir = mkdtempSync(join(tmpdir(), 'dsh-recollect-verify-'));
const config = mod.resolveConfig({ userDir: tempUserDir }, {
  platform: process.platform,
  env: process.env,
});
const workspaceRoot = repoRoot;

function spawnChild(binary, argv) {
  return spawn(binary, argv, {
    cwd: workspaceRoot,
    stdio: ['pipe', 'pipe', 'pipe'],
    windowsHide: true,
  });
}

function rpcChild(binary) {
  const child = spawnChild(binary, []);
  let buffer = '';
  const pending = new Map();
  let nextID = 1;
  let failure = null;
  child.stdout.setEncoding('utf8');
  child.stdout.on('data', (chunk) => {
    buffer += chunk;
    let at = buffer.indexOf('\n');
    while (at >= 0) {
      const line = buffer.slice(0, at);
      buffer = buffer.slice(at + 1);
      at = buffer.indexOf('\n');
      let reply;
      try { reply = JSON.parse(line); } catch { continue; }
      const waiting = pending.get(Number(reply.id));
      if (!waiting) continue;
      pending.delete(Number(reply.id));
      if (reply.error) waiting.reject(new Error(reply.error.message));
      else waiting.resolve(reply.result);
    }
  });
  child.on('error', (error) => { failure = error; });
  return {
    request(method, params = {}) {
      if (failure) throw failure;
      const id = nextID++;
      const line = JSON.stringify({ jsonrpc: '2.0', id, method, params }) + '\n';
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          pending.delete(id);
          reject(new Error(`${method} timed out`));
        }, 5000);
        pending.set(id, {
          resolve: (value) => { clearTimeout(timer); resolve(value); },
          reject: (error) => { clearTimeout(timer); reject(error); },
        });
        child.stdin.write(line);
      });
    },
    close() { child.stdin.end(); child.kill(); },
  };
}

let roundTripFailure = null;
let client;
try {
  const resolver = spawnChild(config.binaryPath, ['resolve', '--root', workspaceRoot, '--json']);
  let resolvedText = '';
  resolver.stdout.setEncoding('utf8');
  resolver.stdout.on('data', (chunk) => { resolvedText += chunk; });
  const resolved = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('recollect resolve timed out')), 5000);
    resolver.on('error', reject);
    resolver.on('close', (code) => {
      clearTimeout(timer);
      if (code !== 0) return reject(new Error(`recollect resolve exited ${code}`));
      try { resolve(JSON.parse(resolvedText)); } catch (error) { reject(error); }
    });
  });
  const projectRoot = resolved.projectRoot;
  const projectDir = mod.projectDirFor(config.userDir, projectRoot, process.platform);
  check('engine canonicalization drives the store path', () => {
    if (!projectRoot) throw new Error('empty canonical root');
    if (projectDir === mod.projectDirFor(config.userDir, join(workspaceRoot, '..', 'other'), process.platform)) {
      throw new Error('store path did not change with canonical root');
    }
  });

  client = rpcChild(config.binaryPath);
  const init = await client.request('initialize', {
    projectDir,
    projectRoot,
    userDir: config.userDir,
    harnessID: 'dsh-workspace',
    capabilities: ['context'],
    generationState: 'disabled',
    protocol: { major: 1, minor: 1 },
  });
  check('engine accepts plugin initialize bindings', () => {
    if (init.projectDir !== projectDir) throw new Error('projectDir not honored');
    if (init.userDir !== config.userDir) throw new Error('userDir not honored');
    if (init.harnessID !== 'dsh-workspace') throw new Error('harnessID not honored');
    if (!init.protocol || init.protocol.major !== 1) throw new Error('protocol mismatch');
  });

  const search = await client.request('memory.execute', {
    target: 'project', operation: 'search', query: 'recollect',
  });
  check('engine serves a read-only memory call', () => {
    if (search.operation !== 'search') throw new Error('unexpected operation result');
  });

  const context = await client.request('context.get', {});
  check('engine renders a memory snapshot', () => {
    if (typeof context.section !== 'string' || typeof context.snapshotID !== 'string') {
      throw new Error('context.get result is incomplete');
    }
  });
} catch (error) {
  roundTripFailure = error;
  check('engine round trip', () => { throw error; });
} finally {
  client?.close();
  rmSync(tempUserDir, { recursive: true, force: true });
}

let passed = 0;
let failed = 0;
for (const [name, fn] of checks) {
  try {
    await fn();
    console.log(`PASS ${name}`);
    passed += 1;
  } catch (error) {
    console.error(`FAIL ${name}: ${error.message}`);
    failed += 1;
  }
}
console.log(`${passed}/${checks.length} checks passed`);
if (failed) process.exitCode = 1;
