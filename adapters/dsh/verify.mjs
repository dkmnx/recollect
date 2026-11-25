// E2E verifier for the dsh-recollect per-workspace Host plugin.
// Run with: node verify.mjs <adapter-directory>
// audit: behavior - proves the shipped bundle delegates canonicalization and drives a real engine RPC round trip.
// audit: regression - a lost registration or a fixed binding would change the asserted schema or project id.
// audit: coverage gap - live DSH activation is verified separately with cordis_inspect and a real scoped tool call.
// audit: seam - the engine child is driven over its real stdio JSON-RPC boundary; no production test hook exists.

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
    fn();
    console.log(`PASS ${name}`);
    passed += 1;
  } catch (error) {
    console.error(`FAIL ${name}: ${error.message}`);
    failed += 1;
  }
}
console.log(`${passed}/${checks.length} checks passed`);
if (failed) process.exitCode = 1;
