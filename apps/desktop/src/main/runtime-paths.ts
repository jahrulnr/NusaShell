import { resolve } from "node:path";

export interface RuntimePathOptions {
  readonly isPackaged: boolean;
  readonly moduleDir: string;
  readonly resourcesPath: string;
}

export interface RuntimePaths {
  readonly pluginsRoot: string;
  readonly promptsRoot: string;
  readonly docsRoot: string;
}

export function resolveRuntimePaths({
  isPackaged,
  moduleDir,
  resourcesPath,
}: RuntimePathOptions): RuntimePaths {
  const repositoryRoot = resolve(moduleDir, "..", "..", "..", "..");
  const agentRoot = isPackaged
    ? resolve(resourcesPath, "agent")
    : resolve(repositoryRoot, "resources", "agent");

  return {
    pluginsRoot: isPackaged
      ? resolve(resourcesPath, "plugins")
      : resolve(repositoryRoot, "plugins"),
    promptsRoot: resolve(agentRoot, "prompts"),
    docsRoot: resolve(agentRoot, "docs"),
  };
}
