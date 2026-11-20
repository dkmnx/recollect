// Resolves plugin options and host location into bound engine roots.

import { createHash } from "node:crypto";
import { posix, win32 } from "node:path";

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

export function resolveConfig(
  options: PluginOptions,
  location: HostLocation,
  host: HostEnv,
): ResolvedConfig {
  const binaryPath = options.binaryPath?.trim();
  if (!binaryPath) throw new ConfigError("binaryPath is required");

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
