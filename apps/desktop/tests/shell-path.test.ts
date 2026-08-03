import { describe, expect, it } from "vitest";
import {
  enrichProcessPathFromLoginShell,
  mergePathSegments,
} from "../src/main/shell-path.js";

describe("mergePathSegments", () => {
  it("dedupes login extras after existing Electron PATH", () => {
    expect(mergePathSegments("/usr/bin:/bin", "/home/u/.nvm/bin:/usr/bin")).toBe(
      "/usr/bin:/bin:/home/u/.nvm/bin",
    );
  });
});

describe("enrichProcessPathFromLoginShell", () => {
  it("no-ops on Windows", async () => {
    const env = { PATH: "/usr/bin" };
    const result = await enrichProcessPathFromLoginShell({
      platform: "win32",
      env,
      readLoginPath: async () => "/home/u/.nvm/bin",
    });
    expect(result.enriched).toBe(false);
    expect(env.PATH).toBe("/usr/bin");
  });

  it("merges login PATH into env", async () => {
    const env = { PATH: "/usr/bin:/bin" };
    const result = await enrichProcessPathFromLoginShell({
      platform: "linux",
      env,
      readLoginPath: async () => "/home/u/.nvm/versions/node/v24/bin:/usr/bin",
    });
    expect(result.enriched).toBe(true);
    expect(env.PATH).toBe("/usr/bin:/bin:/home/u/.nvm/versions/node/v24/bin");
  });

  it("swallows login-shell failures", async () => {
    const env = { PATH: "/usr/bin" };
    const result = await enrichProcessPathFromLoginShell({
      platform: "linux",
      env,
      readLoginPath: async () => {
        throw new Error("shell unavailable");
      },
    });
    expect(result.enriched).toBe(false);
    expect(env.PATH).toBe("/usr/bin");
  });
});
