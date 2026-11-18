// Resolves plugin options and host location into bound engine roots.

import { posix, win32 } from "node:path";

export interface PluginOptions {
  binaryPath?: string;
  userDir?: string;
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
  userDir: string;
  directory: string;
  workspaceID: string;
  harnessID: string;
  capabilities: string[];
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

  const projectDir = location.project?.canonical?.trim();
  if (!projectDir) throw new ConfigError("project canonical root is required");

  return {
    binaryPath,
    projectDir,
    userDir: options.userDir?.trim() || computeUserRoot(host.platform, host.env),
    directory: location.directory ?? projectDir,
    workspaceID: location.workspaceID ?? "",
    harnessID: "opencode",
    capabilities: ["generation", "events", "context"],
  };
}
