import { delimiter as pathDelimiter } from "node:path";
import { describe, expect, it } from "vitest";
import {
  commandDirForPath,
  enrichSpawnEnv,
  formatSpawnEnoentHint,
  mergePathSegments,
} from "../src/process/spawn-env.js";
import { resolveStdioLaunch } from "../src/mcp/stdio-mcp-client.adapter.js";

const d = pathDelimiter;

describe("mergePathSegments", () => {
  it("dedupes while preserving first-seen order", () => {
    expect(mergePathSegments(`/a${d}/b`, `/b${d}/c`, "/a")).toBe(`/a${d}/b${d}/c`);
  });

  it("ignores empty segments", () => {
    expect(mergePathSegments(`/a${d}${d}/b`, undefined, "")).toBe(`/a${d}/b`);
  });
});

describe("commandDirForPath", () => {
  it("returns dirname for absolute commands", () => {
    expect(commandDirForPath("/home/user/.nvm/versions/node/v24/bin/npx")).toBe(
      "/home/user/.nvm/versions/node/v24/bin",
    );
  });

  it("returns undefined for bare commands", () => {
    expect(commandDirForPath("npx")).toBeUndefined();
    expect(commandDirForPath("node")).toBeUndefined();
  });
});

describe("enrichSpawnEnv", () => {
  it("prepends absolute command dirname to PATH", () => {
    const env = enrichSpawnEnv("/opt/nvm/bin/npx", {
      PATH: `/usr/bin${d}/bin`,
      FOO: "bar",
    });
    expect(env.FOO).toBe("bar");
    expect(env.PATH?.startsWith(`/opt/nvm/bin${d}`)).toBe(true);
    expect(env.PATH).toContain("/usr/bin");
  });

  it("leaves bare commands without forcing a dirname prepend", () => {
    const env = enrichSpawnEnv("npx", { PATH: "/usr/bin", KEEP: "1" });
    expect(env).toEqual({ PATH: "/usr/bin", KEEP: "1" });
  });
});

describe("resolveStdioLaunch PATH enrichment", () => {
  it("keeps Electron-as-node remap and still enriches PATH for absolute node", () => {
    const launch = resolveStdioLaunch(
      "/opt/nvm/bin/node",
      { PATH: "/usr/bin" },
      { execPath: "/opt/NusaShell/NusaShell", electronVersion: "33.0.0" },
    );
    // Absolute "node" path is not the bare "node" token — no Electron remap.
    expect(launch.command).toBe("/opt/nvm/bin/node");
    expect(launch.env.PATH?.startsWith(`/opt/nvm/bin${d}`)).toBe(true);
  });

  it("remaps bare node under Electron and preserves PATH enrichment from env", () => {
    const launch = resolveStdioLaunch(
      "node",
      { PATH: `/custom/bin${d}/usr/bin`, PLUGIN_ENV: "yes" },
      { execPath: "/opt/NusaShell/NusaShell", electronVersion: "33.0.0" },
    );
    expect(launch.command).toBe("/opt/NusaShell/NusaShell");
    expect(launch.env.ELECTRON_RUN_AS_NODE).toBe("1");
    expect(launch.env.PLUGIN_ENV).toBe("yes");
    expect(launch.env.PATH).toContain("/custom/bin");
  });
});

describe("formatSpawnEnoentHint", () => {
  it("mentions nvm for npx", () => {
    expect(formatSpawnEnoentHint("npx")).toMatch(/nvm|PATH/i);
  });
});
