import { describe, expect, it } from "vitest";
import { detectRuntimeOs } from "../src/index.js";

describe("detectRuntimeOs", () => {
  it("returns windows for win32", () => {
    expect(detectRuntimeOs({ platform: "win32" })).toBe("windows");
  });

  it("returns macos for darwin", () => {
    expect(detectRuntimeOs({ platform: "darwin" })).toBe("macos");
  });

  it("returns linux (distro) from /etc/os-release", () => {
    expect(detectRuntimeOs({
      platform: "linux",
      fileExists: () => false,
      readTextFile: (path) => path === "/etc/os-release" ? 'NAME="Ubuntu"\nID=ubuntu\nVERSION_ID="24.04"\n' : undefined,
    })).toBe("linux (ubuntu)");
  });

  it("returns docker (distro) when /.dockerenv exists", () => {
    expect(detectRuntimeOs({
      platform: "linux",
      fileExists: (path) => path === "/.dockerenv",
      readTextFile: (path) => path === "/etc/os-release" ? "ID=debian\n" : undefined,
    })).toBe("docker (debian)");
  });

  it("detects docker from cgroup when /.dockerenv is missing", () => {
    expect(detectRuntimeOs({
      platform: "linux",
      fileExists: () => false,
      readTextFile: (path) => {
        if (path === "/proc/1/cgroup") return "0::/docker/abc123\n";
        if (path === "/etc/os-release") return "ID=\"centos\"\n";
        return undefined;
      },
    })).toBe("docker (centos)");
  });

  it("falls back to linux when distro cannot be read", () => {
    expect(detectRuntimeOs({
      platform: "linux",
      fileExists: () => false,
      readTextFile: () => undefined,
    })).toBe("linux");
  });

  it("normalizes common distro ID aliases", () => {
    expect(detectRuntimeOs({
      platform: "linux",
      fileExists: () => false,
      readTextFile: () => "ID=amzn\n",
    })).toBe("linux (amazon-linux)");
  });
});
