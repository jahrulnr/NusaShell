import { describe, expect, it } from "vitest";
import path from "node:path";
import {
  wrapToolArgs,
  wrapTerminalArgs,
  wrapFilesArgs,
} from "../src/agent/services/workspace-tool-wrap.js";

const WS = path.resolve("/tmp/proj");

describe("workspace-tool-wrap", () => {
  describe("wrapTerminalArgs", () => {
    it("injects cwd when omitted for exec", () => {
      const result = wrapTerminalArgs("exec", { command: "ls" }, WS);
      expect(result.args).toEqual({ command: "ls", cwd: WS });
      expect(result.rewritten).toEqual(["cwd"]);
    });

    it("injects cwd when empty for open", () => {
      const result = wrapTerminalArgs("open", { cwd: "" }, WS);
      expect(result.args.cwd).toBe(WS);
    });

    it("injects cwd when relative", () => {
      const result = wrapTerminalArgs("exec", { command: "ls", cwd: "subdir" }, WS);
      expect(result.args.cwd).toBe(WS);
    });

    it("preserves an explicit absolute cwd", () => {
      const other = path.resolve("/etc");
      const result = wrapTerminalArgs("exec", { command: "ls", cwd: other }, WS);
      expect(result.args.cwd).toBe(other);
      expect(result.rewritten).toEqual([]);
    });

    it("does not touch non-cwd terminal tools", () => {
      const result = wrapTerminalArgs("write", { sessionId: "x", data: "y" }, WS);
      expect(result.args).toEqual({ sessionId: "x", data: "y" });
      expect(result.rewritten).toEqual([]);
    });
  });

  describe("wrapFilesArgs", () => {
    it("rewrites a relative path to absolute under workspace", () => {
      const result = wrapFilesArgs("read", { path: "src/foo.ts" }, WS);
      expect(result.args.path).toBe(path.join(WS, "src/foo.ts"));
      expect(result.rewritten).toEqual(["path"]);
    });

    it("rewrites source and destination for move", () => {
      const result = wrapFilesArgs("move", { source: "a.txt", destination: "b.txt" }, WS);
      expect(result.args.source).toBe(path.join(WS, "a.txt"));
      expect(result.args.destination).toBe(path.join(WS, "b.txt"));
      expect(result.rewritten).toEqual(["source", "destination"]);
    });

    it("preserves an absolute path", () => {
      const abs = path.resolve("/etc/hosts");
      const result = wrapFilesArgs("read", { path: abs }, WS);
      expect(result.args.path).toBe(abs);
      expect(result.rewritten).toEqual([]);
    });

    it("preserves root marker / and empty", () => {
      expect(wrapFilesArgs("list", { path: "/" }, WS).args.path).toBe("/");
      expect(wrapFilesArgs("list", { path: "" }, WS).args.path).toBe("");
    });

    it("does not touch unknown files tools", () => {
      const result = wrapFilesArgs("files_unknown", { path: "x" }, WS);
      expect(result.args).toEqual({ path: "x" });
      expect(result.rewritten).toEqual([]);
    });
  });

  describe("wrapToolArgs dispatcher", () => {
    it("routes terminal by plugin id", () => {
      expect(wrapToolArgs("nusashell.terminal", "exec", { command: "pwd" }, WS).cwd).toBe(WS);
    });

    it("routes files by plugin id", () => {
      expect(wrapToolArgs("nusashell.files", "read", { path: "a.ts" }, WS).path).toBe(path.join(WS, "a.ts"));
    });

    it("passes third-party plugins through unchanged", () => {
      expect(wrapToolArgs("acme.custom", "do_thing", { path: "rel" }, WS)).toEqual({ path: "rel" });
    });

    it("passes through when workspace is undefined", () => {
      expect(wrapToolArgs("nusashell.terminal", "exec", { command: "pwd" }, undefined)).toEqual({ command: "pwd" });
      expect(wrapToolArgs("nusashell.files", "read", { path: "rel" }, undefined)).toEqual({ path: "rel" });
    });
  });
});
