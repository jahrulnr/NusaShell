import { describe, expect, it } from "vitest";
import { resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { resolveIcon } from "../src/plugin/services/icon-resolver.js";

const INSTALL_PATH = "/home/user/.nusashell/plugins/nusashell.notes";

/** Compute the expected file:// URL the same way resolveIcon does — so tests
 *  pass on both POSIX and Windows (where path.resolve adds a drive letter). */
function expectedFileUrl(installPath: string, relPath: string): string {
  return pathToFileURL(resolve(installPath, relPath)).href;
}

describe("resolveIcon", () => {
  it("passes through emoji/text icons", () => {
    expect(resolveIcon("📝", INSTALL_PATH)).toBe("📝");
    expect(resolveIcon("N", INSTALL_PATH)).toBe("N");
    expect(resolveIcon("Notes", INSTALL_PATH)).toBe("Notes");
  });

  it("passes through HTTP URLs", () => {
    expect(resolveIcon("https://example.com/icon.png", INSTALL_PATH)).toBe(
      "https://example.com/icon.png",
    );
    expect(resolveIcon("http://example.com/icon.png", INSTALL_PATH)).toBe(
      "http://example.com/icon.png",
    );
  });

  it("passes through absolute file:// URLs", () => {
    expect(resolveIcon("file:///abs/path/icon.png", INSTALL_PATH)).toBe(
      "file:///abs/path/icon.png",
    );
  });

  it("resolves file:// relative path against installPath", () => {
    const result = resolveIcon("file://icon.png", INSTALL_PATH);
    expect(result).toBe(expectedFileUrl(INSTALL_PATH, "icon.png"));
  });

  it("resolves file:// nested relative path against installPath", () => {
    const result = resolveIcon("file://assets/icon.png", INSTALL_PATH);
    expect(result).toBe(expectedFileUrl(INSTALL_PATH, "assets/icon.png"));
  });

  it("resolves ./ relative path against installPath", () => {
    const result = resolveIcon("./icon.png", INSTALL_PATH);
    expect(result).toBe(expectedFileUrl(INSTALL_PATH, "icon.png"));
  });

  it("resolves bare filename with extension against installPath", () => {
    const result = resolveIcon("icon.png", INSTALL_PATH);
    expect(result).toBe(expectedFileUrl(INSTALL_PATH, "icon.png"));
  });

  it("resolves /absolute/path against installPath as file:// URL", () => {
    const result = resolveIcon("/abs/path/icon.png", INSTALL_PATH);
    expect(result).toBe(pathToFileURL(resolve("/abs/path/icon.png")).href);
  });

  it("returns empty string for empty input", () => {
    expect(resolveIcon("", INSTALL_PATH)).toBe("");
    expect(resolveIcon("  ", INSTALL_PATH)).toBe("");
  });
});
