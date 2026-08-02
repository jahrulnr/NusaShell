import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { resolveRuntimePaths } from "../src/main/runtime-paths.js";

describe("desktop runtime paths", () => {
  it("resolves packaged plugins and agent resources from Electron resources", () => {
    const resourcesPath = resolve("/opt/NusaShell/resources");

    expect(resolveRuntimePaths({
      isPackaged: true,
      moduleDir: "/unused",
      resourcesPath,
      userDataPath: "/home/user/.config/nusashell",
    })).toEqual({
      pluginsRoot: "/home/user/.config/nusashell/plugins",
      bundledPluginsRoot: join(resourcesPath, "plugins"),
      userPluginsRoot: "/home/user/.config/nusashell/plugins",
      builtinSkillsRoot: join(resourcesPath, "agent", "skills"),
      promptsRoot: join(resourcesPath, "agent", "prompts"),
      docsRoot: join(resourcesPath, "agent", "docs"),
    });
  });

  it("resolves development resources from the repository root", () => {
    const moduleDir = "/repo/apps/desktop/.vite/build";
    expect(resolveRuntimePaths({
      isPackaged: false,
      moduleDir,
      resourcesPath: "/unused",
    })).toEqual({
      pluginsRoot: resolve(moduleDir, "..", "..", "..", "..", ".nusashell", "plugins"),
      bundledPluginsRoot: resolve(moduleDir, "..", "..", "..", "..", "plugins"),
      userPluginsRoot: resolve(moduleDir, "..", "..", "..", "..", ".nusashell", "plugins"),
      builtinSkillsRoot: resolve(moduleDir, "..", "..", "..", "..", "resources", "agent", "skills"),
      promptsRoot: resolve(moduleDir, "..", "..", "..", "..", "resources", "agent", "prompts"),
      docsRoot: resolve(moduleDir, "..", "..", "..", "..", "resources", "agent", "docs"),
    });
  });
});
