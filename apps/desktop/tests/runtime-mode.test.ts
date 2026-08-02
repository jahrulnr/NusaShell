import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  DEV_DATA_DIRNAME,
  DEV_WS_PORT,
  PROD_DATA_DIRNAME,
  PROD_WS_PORT,
  resolveBuildLabel,
  resolveDataRoot,
  resolveIsDev,
  resolveWsPort,
} from "../src/main/runtime-mode.js";

describe("resolveIsDev", () => {
  it("is dev when unpackaged and --dev present", () => {
    expect(resolveIsDev({ isPackaged: false, argv: ["--dev"] })).toBe(true);
  });

  it("is not dev when unpackaged without --dev", () => {
    expect(resolveIsDev({ isPackaged: false, argv: [] })).toBe(false);
  });

  it("is never dev when packaged, even with --dev", () => {
    expect(resolveIsDev({ isPackaged: true, argv: ["--dev"] })).toBe(false);
  });

  it("is never dev when packaged without --dev", () => {
    expect(resolveIsDev({ isPackaged: true, argv: [] })).toBe(false);
  });
});

describe("resolveBuildLabel", () => {
  it("labels dev and production modes correctly", () => {
    expect(resolveBuildLabel(true)).toBe("dev");
    expect(resolveBuildLabel(false)).toBe("production");
  });
});

describe("resolveWsPort", () => {
  it("defaults to 9131 in dev", () => {
    expect(resolveWsPort({ isDev: true })).toBe(DEV_WS_PORT);
  });

  it("defaults to 9130 in prod", () => {
    expect(resolveWsPort({ isDev: false })).toBe(PROD_WS_PORT);
  });

  it("env port always wins when valid", () => {
    expect(resolveWsPort({ isDev: true, envPort: "9200" })).toBe(9200);
    expect(resolveWsPort({ isDev: false, envPort: "9200" })).toBe(9200);
  });

  it("ignores invalid env port and falls back to mode default", () => {
    expect(resolveWsPort({ isDev: true, envPort: "not-a-port" })).toBe(DEV_WS_PORT);
    expect(resolveWsPort({ isDev: false, envPort: "not-a-port" })).toBe(PROD_WS_PORT);
  });

  it("rejects out-of-range env port", () => {
    expect(resolveWsPort({ isDev: false, envPort: "0" })).toBe(PROD_WS_PORT);
    expect(resolveWsPort({ isDev: false, envPort: "70000" })).toBe(PROD_WS_PORT);
  });
});

describe("resolveDataRoot", () => {
  const repositoryRoot = resolve("/repo");
  const appDataPath = resolve("/home/user/.config");

  it("dev data root lives under the repo in .nusashell", () => {
    expect(resolveDataRoot({ isDev: true, repositoryRoot, appDataPath }))
      .toBe(join(repositoryRoot, DEV_DATA_DIRNAME));
  });

  it("prod data root lives under appData/nusashell, never the repo", () => {
    expect(resolveDataRoot({ isDev: false, repositoryRoot, appDataPath }))
      .toBe(join(appDataPath, PROD_DATA_DIRNAME));
  });

  it("prod data root does not reference the repository", () => {
    const root = resolveDataRoot({ isDev: false, repositoryRoot, appDataPath });
    expect(root.startsWith(repositoryRoot)).toBe(false);
  });
});
