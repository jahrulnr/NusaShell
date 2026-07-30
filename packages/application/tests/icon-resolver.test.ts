import { describe, expect, it } from "vitest";
import { resolveIcon } from "../src/plugin/services/icon-resolver.js";

const INSTALL_PATH = "/home/user/.nusashell/plugins/nusashell.notes";

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
    expect(result).toBe(`file://${INSTALL_PATH}/icon.png`);
  });

  it("resolves file:// nested relative path against installPath", () => {
    const result = resolveIcon("file://assets/icon.png", INSTALL_PATH);
    expect(result).toBe(`file://${INSTALL_PATH}/assets/icon.png`);
  });

  it("resolves ./ relative path against installPath", () => {
    const result = resolveIcon("./icon.png", INSTALL_PATH);
    expect(result).toBe(`file://${INSTALL_PATH}/icon.png`);
  });

  it("resolves bare filename with extension against installPath", () => {
    const result = resolveIcon("icon.png", INSTALL_PATH);
    expect(result).toBe(`file://${INSTALL_PATH}/icon.png`);
  });

  it("resolves /absolute/path against installPath as file:// URL", () => {
    const result = resolveIcon("/abs/path/icon.png", INSTALL_PATH);
    expect(result).toBe("file:///abs/path/icon.png");
  });

  it("returns empty string for empty input", () => {
    expect(resolveIcon("", INSTALL_PATH)).toBe("");
    expect(resolveIcon("  ", INSTALL_PATH)).toBe("");
  });
});
