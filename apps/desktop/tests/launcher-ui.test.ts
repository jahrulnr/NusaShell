import { describe, expect, it } from "vitest";
import {
  applyTextEdit,
  countLogsBySource,
  filterLauncherPlugins,
  findOpaqueBounds,
  pluginIconPresentation,
  positionContextMenu,
  providerApiModes,
} from "../src/renderer/launcher-ui.js";

describe("launcher UI helpers", () => {
  it("renders local file URLs as plugin images on Home", () => {
    expect(pluginIconPresentation("file:///opt/nusashell/mail/icon.png")).toEqual({
      kind: "image",
      source: "file:///opt/nusashell/mail/icon.png",
    });
  });

  it("keeps emoji as text and rejects unsupported image schemes", () => {
    expect(pluginIconPresentation("✉")).toEqual({
      kind: "text",
      text: "✉",
    });
    expect(pluginIconPresentation("javascript:alert(1)")).toEqual({
      kind: "text",
      text: "javascript:alert(1)",
    });
  });

  it("finds the visible artwork inside a transparent plugin icon canvas", () => {
    const pixels = new Uint8ClampedArray([
      0, 0, 0, 0,   20, 40, 60, 255, 0, 0, 0, 0,
      0, 0, 0, 0,   20, 40, 60, 180, 0, 0, 0, 0,
    ]);

    expect(findOpaqueBounds(pixels, 3, 2)).toEqual({
      x: 1,
      y: 0,
      width: 1,
      height: 2,
    });
  });

  it("returns no artwork for a fully transparent plugin icon", () => {
    expect(findOpaqueBounds(new Uint8ClampedArray(4 * 4 * 4), 4, 4)).toBeNull();
  });

  it("filters plugins by name, plugin id, and manifest description", () => {
    const plugins = [
      { pluginId: "com.example.notes", name: "Notes", description: "Quick markdown notes" },
      { pluginId: "com.example.browse", name: "Browser", description: "Web automation" },
    ];

    expect(filterLauncherPlugins(plugins, "markdown").map((plugin) => plugin.pluginId)).toEqual(["com.example.notes"]);
    expect(filterLauncherPlugins(plugins, "browse").map((plugin) => plugin.pluginId)).toEqual(["com.example.browse"]);
    expect(filterLauncherPlugins(plugins, "")).toHaveLength(2);
  });

  it("keeps a right-click menu inside the window", () => {
    expect(positionContextMenu({ x: 880, y: 670 }, { width: 220, height: 240 }, { width: 900, height: 700 })).toEqual({ x: 672, y: 452 });
  });

  it("pastes clipboard text at the current selection and moves the caret", () => {
    expect(applyTextEdit(
      { value: "hello world", selectionStart: 6, selectionEnd: 11 },
      "paste",
      "NusaShell",
    )).toEqual({
      value: "hello NusaShell",
      selectionStart: 15,
      selectionEnd: 15,
      clipboardText: "",
    });
  });

  it("cuts and copies only the selected text", () => {
    expect(applyTextEdit(
      { value: "hello world", selectionStart: 0, selectionEnd: 5 },
      "cut",
    )).toEqual({
      value: " world",
      selectionStart: 0,
      selectionEnd: 0,
      clipboardText: "hello",
    });
    expect(applyTextEdit(
      { value: "hello world", selectionStart: 6, selectionEnd: 11 },
      "copy",
    )).toEqual({
      value: "hello world",
      selectionStart: 6,
      selectionEnd: 11,
      clipboardText: "world",
    });
  });

  it("counts retained logs per producer source", () => {
    expect(countLogsBySource([
      { source: "main" },
      { source: "backend" },
      { source: "main" },
      { source: "ipc" },
    ])).toEqual({ all: 4, main: 2, backend: 1, ipc: 1 });
  });

  it("offers both OpenAI-compatible API modes for built-in gateways", () => {
    expect(providerApiModes("openrouter")).toEqual([
      { value: "chat", label: "Chat Completions" },
      { value: "responses", label: "Responses API" },
    ]);
  });

  it("keeps native and custom provider dialects explicit", () => {
    expect(providerApiModes("claude")).toEqual([
      { value: "messages", label: "Anthropic Messages" },
    ]);
    expect(providerApiModes("openai-compatible")).toEqual([
      { value: "chat", label: "Chat Completions" },
      { value: "responses", label: "Responses API" },
      { value: "messages", label: "Anthropic Messages" },
    ]);
  });
});
