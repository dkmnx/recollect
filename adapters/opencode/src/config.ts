// Resolves plugin options and host location into bound engine roots.

import { createHash } from "node:crypto";
import { accessSync, constants, statSync } from "node:fs";
import { homedir } from "node:os";
import { join, posix, win32 } from "node:path";

export interface PluginOptions {
  binaryPath?: string;
  userDir?: string;
  generationPolicy?: string;
  generationTimeoutMs?: number;
}

export interface HostLocation {
  project: { canonical: string };
  directory?: string;
  workspaceID?: string;
}

export interface HostEnv {
  platform: string;
  env: Record<string, string | undefined>;
}

export interface ResolvedConfig {
  binaryPath: string;
  projectDir: string;
  projectRoot: string;
  userDir: string;
  directory: string;
  workspaceID: string;
  harnessID: string;
  capabilities: string[];
  generationPolicy: string;
  generationTimeoutMs: number;
}

export class ConfigError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "ConfigError";
  }
}

export function computeUserRoot(
  platform: string,
  env: Record<string, string | undefined>,
): string {
  if (platform === "win32") {
    const local = env.LOCALAPPDATA ??
      (env.USERPROFILE ? win32.join(env.USERPROFILE, "AppData", "Local") : "C:\\Users\\default\\AppData\\Local");
    return win32.join(local, "recollect");
  }
  return posix.join(env.HOME ?? "~", ".recollect");
}

function isExecutable(path: string, platform: string): boolean {
  try {
    const info = statSync(path);
    if (!info.isFile()) return false;
    if (platform !== "win32") accessSync(path, constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

function findOnPath(host: HostEnv): string {
  const raw = host.env.PATH ?? host.env.Path ?? "";
  const separator = host.platform === "win32" ? ";" : ":";
  const extensions = host.platform === "win32"
    ? (host.env.PATHEXT ?? ".EXE;.CMD;.BAT;.COM")
      .split(";")
      .map((extension) => extension.trim())
      .filter(Boolean)
    : [""];
  for (const entry of raw.split(separator)) {
    const dir = entry.replace(/^"|"$/g, "").trim();
    if (!dir) continue;
    for (const extension of extensions) {
      const candidate = join(dir, "recollect" + extension);
      if (isExecutable(candidate, host.platform)) return candidate;
    }
  }
  return "";
}

function defaultBinaryPath(host: HostEnv): string {
  if (host.platform === "win32") {
    const base = host.env.LOCALAPPDATA ??
      (host.env.USERPROFILE
        ? join(host.env.USERPROFILE, "AppData", "Local")
        : join(homedir(), "AppData", "Local"));
    return join(base, "Programs", "recollect", "recollect.exe");
  }
  return join(host.env.HOME ?? homedir(), ".local", "bin", "recollect");
}

function resolveBinaryPath(option: string | undefined, host: HostEnv): string {
  const explicit = option?.trim();
  if (explicit) return explicit;
  const configured = host.env.RECOLLECT_BINARY?.trim();
  if (configured) return configured;
  const onPath = findOnPath(host);
  if (onPath) return onPath;
  const fallback = defaultBinaryPath(host);
  if (isExecutable(fallback, host.platform)) return fallback;
  throw new ConfigError(
    "cannot find the recollect binary; set binaryPath, RECOLLECT_BINARY, or add recollect to PATH",
  );
}

export function resolveConfig(
  options: PluginOptions,
  location: HostLocation,
  host: HostEnv,
): ResolvedConfig {
  const binaryPath = resolveBinaryPath(options.binaryPath, host);

  const projectRoot = location.project?.canonical?.trim();
  if (!projectRoot) throw new ConfigError("project canonical root is required");
  const userDir = options.userDir?.trim() || computeUserRoot(host.platform, host.env);
  const joinPath = host.platform === "win32" ? win32.join : posix.join;
  const projectID = createHash("sha256").update(projectRoot).digest("hex").slice(0, 16);
  const projectDir = joinPath(userDir, "projects", projectID);

  const generationPolicy = options.generationPolicy ?? "host";
  if (generationPolicy !== "host" && generationPolicy !== "off") {
    throw new ConfigError("generationPolicy must be host or off");
  }
  const generationTimeoutMs = options.generationTimeoutMs ?? 120000;
  if (!Number.isInteger(generationTimeoutMs) || generationTimeoutMs < 1 || generationTimeoutMs > 120000) {
    throw new ConfigError("generationTimeoutMs must be an integer from 1 through 120000");
  }

  return {
    binaryPath,
    projectDir,
    projectRoot,
    userDir,
    directory: location.directory ?? projectRoot,
    workspaceID: location.workspaceID ?? "",
    harnessID: "opencode",
    capabilities: ["generation", "events", "context"],
    generationPolicy,
    generationTimeoutMs,
  };
}
