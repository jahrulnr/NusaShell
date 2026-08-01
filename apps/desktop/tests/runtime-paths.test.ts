import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { resolveRuntimePaths } from "../src/main/runtime-paths.js";

describe("desktop runtime paths", () => {
  it("resolves packaged plugins and agent resources from Electron resources", () => {
    expect(resolveRuntimePaths({
      isPackaged: true,
      moduleDir: "/unused",
      resourcesPath: "/opt/NusaShell/resources",
    })).toEqual({
      pluginsRoot: join("/opt/NusaShell/resources", "plugins"),
      promptsRoot: join("/opt/NusaShell/resources", "agent", "prompts"),
      docsRoot: join("/opt/NusaShell/resources", "agent", "docs"),
    });
  });

  it("resolves development resources from the repository root", () => {
    const moduleDir = "/repo/apps/desktop/.vite/build";
    expect(resolveRuntimePaths({
      isPackaged: false,
      moduleDir,
      resourcesPath: "/unused",
    })).toEqual({
      pluginsRoot: resolve(moduleDir, "..", "..", "..", "..", "plugins"),
      promptsRoot: resolve(moduleDir, "..", "..", "..", "..", "resources", "agent", "prompts"),
      docsRoot: resolve(moduleDir, "..", "..", "..", "..", "resources", "agent", "docs"),
    });
  });
});
