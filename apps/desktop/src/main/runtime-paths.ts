import { resolve } from "node:path";

export interface RuntimePathOptions {
  readonly isPackaged: boolean;
  readonly moduleDir: string;
  readonly resourcesPath: string;
  readonly userDataPath?: string;
}

export interface RuntimePaths {
  readonly pluginsRoot: string;
  readonly bundledPluginsRoot: string;
  readonly userPluginsRoot: string;
  readonly builtinSkillsRoot: string;
  readonly promptsRoot: string;
  readonly docsRoot: string;
}

export function resolveRuntimePaths({
  isPackaged,
  moduleDir,
  resourcesPath,
  userDataPath,
}: RuntimePathOptions): RuntimePaths {
  const repositoryRoot = resolve(moduleDir, "..", "..", "..", "..");
  const agentRoot = isPackaged
    ? resolve(resourcesPath, "agent")
    : resolve(repositoryRoot, "resources", "agent");

  const bundledPluginsRoot = isPackaged
    ? resolve(resourcesPath, "plugins")
    : resolve(repositoryRoot, "plugins");
  const userPluginsRoot = userDataPath
    ? resolve(userDataPath, "plugins")
    : resolve(repositoryRoot, ".nusashell", "plugins");

  return {
    pluginsRoot: userPluginsRoot,
    bundledPluginsRoot,
    userPluginsRoot,
    builtinSkillsRoot: resolve(agentRoot, "skills"),
    promptsRoot: resolve(agentRoot, "prompts"),
    docsRoot: resolve(agentRoot, "docs"),
  };
}
