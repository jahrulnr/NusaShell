import { delimiter as pathDelimiter, join } from "node:path";
import { homedir } from "node:os";
import { describe, expect, it } from "vitest";
import {
  commandDirForPath,
  enrichSpawnEnv,
  expandTilde,
  expandTildeInPath,
  formatSpawnEnoentHint,
  mergePathSegments,
} from "../src/process/spawn-env.js";
import { resolveStdioLaunch } from "../src/mcp/stdio-mcp-client.adapter.js";

const d = pathDelimiter;
const home = homedir();

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

describe("expandTilde", () => {
  it("expands ~/ to home directory", () => {
    expect(expandTilde("~/.local/bin/messager-mcp")).toBe(join(home, ".local/bin/messager-mcp"));
    expect(expandTilde("~/bin")).toBe(join(home, "bin"));
  });

  it("expands bare ~ to home directory", () => {
    expect(expandTilde("~")).toBe(home);
  });

  it("leaves absolute paths unchanged", () => {
    expect(expandTilde("/usr/bin/node")).toBe("/usr/bin/node");
  });

  it("leaves bare commands unchanged", () => {
    expect(expandTilde("node")).toBe("node");
    expect(expandTilde("npx")).toBe("npx");
  });

  it("leaves ~user/... as-is (Node can't resolve other users)", () => {
    expect(expandTilde("~root/bin")).toBe("~root/bin");
  });

  it("returns undefined for undefined input", () => {
    expect(expandTilde(undefined)).toBeUndefined();
  });
});

describe("expandTildeInPath", () => {
  it("expands ~ in every PATH segment", () => {
    const result = expandTildeInPath(`~/.local/bin${d}/usr/bin${d}~/bin`);
    expect(result).toBe(`${join(home, ".local/bin")}${d}/usr/bin${d}${join(home, "bin")}`);
  });

  it("leaves PATH without tilde unchanged", () => {
    expect(expandTildeInPath(`/usr/bin${d}/bin`)).toBe(`/usr/bin${d}/bin`);
  });

  it("returns undefined for undefined input", () => {
    expect(expandTildeInPath(undefined)).toBeUndefined();
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
    expect(env.KEEP).toBe("1");
    expect(env.PATH).toContain("/usr/bin");
  });

  it("expands ~ in PATH entries from manifest env", () => {
    const env = enrichSpawnEnv("messager-mcp", {
      PATH: `~/.local/bin${d}/usr/bin`,
    });
    expect(env.PATH).toContain(join(home, ".local/bin"));
    expect(env.PATH).not.toContain("~/.local/bin");
  });
});

describe("resolveStdioLaunch tilde expansion", () => {
  it("expands ~ in command path (the bug: ~/.local/bin/messager-mcp)", () => {
    const launch = resolveStdioLaunch(
      "~/.local/bin/messager-mcp",
      { PATH: "/usr/bin" },
      { execPath: "/opt/NusaShell/NusaShell" },
    );
    expect(launch.command).toBe(join(home, ".local/bin/messager-mcp"));
    expect(launch.command).not.toContain("~");
  });

  it("expands ~ in command and also enriches PATH with the command dir", () => {
    const launch = resolveStdioLaunch(
      "~/.local/bin/messager-mcp",
      { PATH: "/usr/bin" },
      { execPath: "/opt/NusaShell/NusaShell" },
    );
    // After tilde expansion, command is absolute → dirname prepended to PATH
    expect(launch.env.PATH).toContain(join(home, ".local/bin"));
  });

  it("leaves bare commands unchanged (no tilde to expand)", () => {
    const launch = resolveStdioLaunch(
      "node",
      { PATH: "/usr/bin" },
      { execPath: "/opt/NusaShell/NusaShell", electronVersion: "33.0.0" },
    );
    expect(launch.command).toBe("/opt/NusaShell/NusaShell");
    expect(launch.env.ELECTRON_RUN_AS_NODE).toBe("1");
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
