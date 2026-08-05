// @vitest-environment jsdom
/**
 * Human-facing edge cases: attachments, workspace, backend error → UI.
 * Mapped from tmp/plan/agent-ui-bh-catalog.md (BH-AGENT-16+).
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentConversationController } from "../src/renderer/agent-conversation-controller.js";
import {
  formatSubagentError,
  formatTurnError,
} from "../src/renderer/agent-conversation-ui.js";
import { inspectAttachmentContent } from "../src/renderer/attachment-content.js";

function installDom() {
  document.body.innerHTML = `
    <div id="agent-thread"></div>
    <div id="agent-conversation-list"></div>
    <span id="agent-conversation-count"></span>
    <input id="agent-conversation-search" value="">
    <input id="agent-input">
    <button id="agent-send-btn"></button>
    <button id="agent-stop-btn" hidden></button>
    <span id="agent-provider-status"></span>
    <span id="agent-workspace-label"></span>
    <button id="agent-workspace-btn" title=""></button>
    <div id="agent-attachments"></div>
    <input id="agent-file-input" type="file">
    <div id="acp-status-bar" hidden>
      <span id="acp-status-provider"></span>
      <span id="acp-status-chip"></span>
    </div>
    <button id="agent-acp-pill" hidden><span id="agent-acp-pill-label"></span></button>
  `;
  globalThis.$ = (selector: string) => document.querySelector(selector);
  window.matchMedia = vi.fn(() => ({
    matches: true,
    media: "",
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })) as typeof window.matchMedia;
}

function fakeFile(name: string, bytes: Uint8Array, size?: number): File {
  const blob = new Blob([bytes]);
  const file = new File([blob], name, { type: "application/octet-stream" });
  if (size !== undefined) {
    Object.defineProperty(file, "size", { value: size });
  }
  return file;
}

function textFile(name: string, text: string) {
  return fakeFile(name, new TextEncoder().encode(text));
}

function pngFile(name = "shot.png") {
  return fakeFile(name, new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10, 0, 0]));
}

describe("BH-AGENT error mapping (human copy from backend shapes)", () => {
  it("BH-AGENT-25 surfaces details.cause next to the short message", () => {
    expect(formatTurnError({
      message: "Provider call failed",
      details: { cause: "HTTP 429 rate limit from upstream" },
    })).toBe("Provider call failed: HTTP 429 rate limit from upstream");
  });

  it("BH-AGENT-26 does not double-append an already-included cause", () => {
    expect(formatTurnError({
      message: "Provider call failed: HTTP 429 rate limit",
      details: { cause: "HTTP 429 rate limit" },
    })).toBe("Provider call failed: HTTP 429 rate limit");
  });

  it("BH-AGENT-27 falls back when message is empty", () => {
    expect(formatTurnError({})).toBe("Unknown error");
    expect(formatTurnError(null)).toBe("Unknown error");
  });

  it("BH-AGENT-27b never shows [object Object] for IPC timeout shapes", () => {
    expect(formatTurnError({ message: "[object Object]", code: "TIMEOUT" })).toBe("TIMEOUT");
    expect(formatTurnError({
      kind: "response",
      ok: false,
      error: { code: "TIMEOUT", message: "IPC request timed out after 1800000ms" },
    })).toBe("IPC request timed out after 1800000ms");
    expect(formatTurnError("[object Object]")).toBe("Unknown error");
  });

  it("BH-AGENT-28 never stringifies subagent errors as [object Object]", () => {
    expect(formatSubagentError({ message: "quota exceeded" })).toBe("quota exceeded");
    expect(formatSubagentError("spawn failed")).toBe("spawn failed");
    expect(formatSubagentError({ code: "ECONNREFUSED", errno: -111 })).toContain("ECONNREFUSED");
    expect(formatSubagentError({ message: "" })).not.toBe("[object Object]");
  });
});

describe("BH-AGENT attachment composer edges", () => {
  beforeEach(() => installDom());

  function makeController(extra: Record<string, unknown> = {}) {
    const notify = vi.fn();
    const controller = new AgentConversationController({
      shell: { agentConversations: {} },
      getActiveModel: () => ({ id: "gpt-test", inputModes: ["text", "image", "file"] }),
      getVisionMode: () => "auto",
      notify,
      log: vi.fn(),
      ...extra,
    } as never);
    return { controller, notify };
  }

  it("BH-AGENT-16 stages a UTF-8 source file as a TXT chip", async () => {
    const { controller } = makeController();
    await controller.addAttachments([textFile("theme.css", ".root { color: red; }")]);
    expect(controller.attachments).toHaveLength(1);
    expect(controller.attachments[0]).toMatchObject({ type: "text", name: "theme.css" });
    expect(document.querySelector("#agent-attachments")?.textContent).toMatch(/TXT.*theme\.css/);
  });

  it("BH-AGENT-17 blocks a fifth attachment with a human limit toast", async () => {
    const { controller, notify } = makeController();
    for (let i = 0; i < 4; i += 1) {
      await controller.addAttachments([textFile(`f${i}.txt`, `body ${i}`)]);
    }
    await controller.addAttachments([textFile("fifth.txt", "nope")]);
    expect(controller.attachments).toHaveLength(4);
    expect(notify).toHaveBeenCalledWith("A turn can include up to 4 attachments.", "error");
  });

  it("BH-AGENT-18 rejects unsupported binary without staging a chip", async () => {
    const { controller, notify } = makeController();
    await controller.addAttachments([fakeFile("blob.bin", new Uint8Array([0, 159, 255, 1, 2, 3]))]);
    expect(controller.attachments).toHaveLength(0);
    expect(notify).toHaveBeenCalledWith(
      expect.stringMatching(/blob\.bin.*not a supported/i),
      "error",
    );
  });

  it("BH-AGENT-19 rejects images when vision mode is off", async () => {
    const { controller, notify } = makeController({
      getVisionMode: () => "off",
      getActiveModel: () => ({ id: "text-only", inputModes: ["text"] }),
    });
    await controller.addAttachments([pngFile()]);
    expect(controller.attachments).toHaveLength(0);
    expect(notify).toHaveBeenCalledWith(
      expect.stringMatching(/text-only.*image input disabled/i),
      "error",
    );
  });

  it("BH-AGENT-20 rejects files larger than 4 MiB by name", async () => {
    const { controller, notify } = makeController();
    const big = fakeFile("huge.pdf", new TextEncoder().encode("%PDF-1.7\n"), 4 * 1024 * 1024 + 1);
    await controller.addAttachments([big]);
    expect(controller.attachments).toHaveLength(0);
    expect(notify).toHaveBeenCalledWith("huge.pdf is larger than 4 MiB.", "error");
  });

  it("BH-AGENT-21 allows submit with only attachments (empty textarea)", async () => {
    const runTurn = vi.fn(async () => ({
      text: "Got the file.",
      traceId: "t-attach",
      rounds: 1,
      model: "gpt-test",
    }));
    const append = vi.fn(async (_id: string, message: unknown) => ({
      id: "room-a",
      kind: "agent",
      messages: [
        { role: "user", content: "", attachments: (message as { attachments: unknown[] }).attachments },
        { role: "assistant", content: "Got the file.", traceId: "t-attach" },
      ],
      workspace: undefined,
    }));
    const get = vi.fn(async () => ({
      id: "room-a",
      kind: "agent",
      messages: [
        { role: "user", content: "", attachments: [{ type: "text", name: "a.txt", content: "x" }] },
        { role: "assistant", content: "Got the file.", traceId: "t-attach" },
      ],
    }));
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          append,
          get,
          list: vi.fn(async () => []),
        },
      },
      runTurn,
      getActiveModel: () => ({
        id: "gpt-test",
        key: "gpt-test",
        providerId: "p",
        contextWindow: 128000,
        inputModes: ["text"],
      }),
      getVisionMode: () => "auto",
      notify: vi.fn(),
      log: vi.fn(),
    } as never);
    controller.conversation = {
      id: "room-a",
      kind: "agent",
      messages: [],
    } as never;
    controller.attachments = [{ type: "text", content: "hi", mediaType: "text/plain", name: "a.txt" }];
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    input.value = "";
    await controller.submit();
    expect(runTurn).toHaveBeenCalled();
    expect(append).toHaveBeenCalled();
  });
});

describe("BH-AGENT workspace edges", () => {
  beforeEach(() => installDom());

  it("BH-AGENT-22 updates label to basename after pick", async () => {
    const setWorkspace = vi.fn(async (_id: string, workspace: string) => ({
      id: "room-a",
      kind: "agent",
      messages: [],
      workspace,
    }));
    const controller = new AgentConversationController({
      shell: {
        shellControls: { pickPluginSource: vi.fn(async () => "/home/me/projects/myapp") },
        agentConversations: { setWorkspace },
      },
      getActiveModel: () => null,
      notify: vi.fn(),
      log: vi.fn(),
    } as never);
    controller.conversation = { id: "room-a", kind: "agent", messages: [] } as never;
    await controller.chooseWorkspace();
    expect(setWorkspace).toHaveBeenCalledWith("room-a", "/home/me/projects/myapp");
    expect(document.querySelector("#agent-workspace-label")?.textContent).toBe("myapp");
    expect((document.querySelector("#agent-workspace-btn") as HTMLButtonElement).title).toContain("/home/me/projects/myapp");
  });

  it("BH-AGENT-23 cancel picker keeps previous workspace", async () => {
    const setWorkspace = vi.fn();
    const controller = new AgentConversationController({
      shell: {
        shellControls: { pickPluginSource: vi.fn(async () => null) },
        agentConversations: { setWorkspace },
      },
      getActiveModel: () => null,
      notify: vi.fn(),
      log: vi.fn(),
    } as never);
    controller.conversation = {
      id: "room-a",
      kind: "agent",
      messages: [],
      workspace: "/tmp/keep",
    } as never;
    controller.updateWorkspaceLabel();
    await controller.chooseWorkspace();
    expect(setWorkspace).not.toHaveBeenCalled();
    expect(document.querySelector("#agent-workspace-label")?.textContent).toBe("keep");
  });

  it("BH-AGENT-24 renders Windows path basename", () => {
    const controller = new AgentConversationController({
      shell: {},
      getActiveModel: () => null,
      log: vi.fn(),
    } as never);
    controller.conversation = {
      id: "room-a",
      kind: "agent",
      messages: [],
      workspace: "D:\\work\\myapp",
    } as never;
    controller.updateWorkspaceLabel();
    expect(document.querySelector("#agent-workspace-label")?.textContent).toBe("myapp");
  });
});

describe("BH-AGENT submit failure mapping (backend error on screen)", () => {
  beforeEach(() => installDom());

  it("BH-AGENT-29 shows Turn failed + cause and leaves Retry when provider dies before stream", async () => {
    const runTurn = vi.fn(async () => {
      const error = new Error("Provider call failed");
      (error as Error & { code?: string; details?: unknown }).code = "AGENT_PROVIDER_FAILED";
      (error as Error & { details?: unknown }).details = { cause: "HTTP 402 payment required" };
      throw error;
    });
    const append = vi.fn(async (_id: string, message: unknown) => ({
      id: "room-a",
      kind: "agent",
      messages: [message],
    }));
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          append,
          get: vi.fn(async () => ({
            id: "room-a",
            kind: "agent",
            messages: [{ role: "user", content: "please help" }],
          })),
          list: vi.fn(async () => []),
        },
      },
      runTurn,
      getActiveModel: () => ({
        id: "m",
        key: "m",
        providerId: "p",
        contextWindow: 8_000,
        inputModes: ["text"],
      }),
      notify: vi.fn(),
      log: vi.fn(),
    } as never);
    controller.conversation = { id: "room-a", kind: "agent", messages: [] } as never;
    const input = document.querySelector<HTMLInputElement>("#agent-input")!;
    input.value = "please help";
    await controller.submit();

    const failed = document.querySelector("#agent-thread article.agent-message-error");
    expect(failed?.textContent).toMatch(/Turn failed:.*Provider call failed.*HTTP 402/i);
    expect(failed?.querySelector(".agent-retry-btn")).not.toBeNull();
    expect(controller.turnPending).toBe(false);
    expect(input.disabled).toBe(false);
    expect(document.querySelector("#agent-provider-status")?.textContent).toMatch(/ready to retry/i);
  });

  it("BH-AGENT-30 tells the user when the reply completed but local save failed", async () => {
    const notify = vi.fn();
    const runTurn = vi.fn(async () => ({
      text: "Here is the answer.",
      traceId: "t-savefail",
      rounds: 1,
      model: "m",
    }));
    const append = vi.fn()
      .mockResolvedValueOnce({
        id: "room-a",
        kind: "agent",
        messages: [{ role: "user", content: "q" }],
      })
      .mockRejectedValueOnce(new Error("disk full"));
    const get = vi.fn(async () => ({
      id: "room-a",
      kind: "agent",
      // Sealed-by-main would skip append; force renderer append path.
      messages: [{ role: "user", content: "q" }],
    }));
    const controller = new AgentConversationController({
      shell: {
        agentConversations: { append, get, list: vi.fn(async () => []) },
      },
      runTurn,
      getActiveModel: () => ({
        id: "m",
        key: "m",
        providerId: "p",
        contextWindow: 8_000,
        inputModes: ["text"],
      }),
      notify,
      log: vi.fn(),
    } as never);
    controller.conversation = { id: "room-a", kind: "agent", messages: [] } as never;
    document.querySelector<HTMLInputElement>("#agent-input")!.value = "q";
    await controller.submit();
    expect(notify).toHaveBeenCalledWith(
      "The response completed but could not be saved locally.",
      "error",
    );
  });

  it("BH-AGENT-31 keeps staged attachments when opening another room without sending", async () => {
    const rooms = {
      "room-a": { id: "room-a", kind: "agent", title: "A", messages: [], createdAt: "", updatedAt: "", messageCount: 0 },
      "room-b": { id: "room-b", kind: "agent", title: "B", messages: [], createdAt: "", updatedAt: "", messageCount: 0 },
    };
    const controller = new AgentConversationController({
      shell: {
        agentConversations: {
          get: vi.fn(async (id: string) => rooms[id as keyof typeof rooms]),
          list: vi.fn(async () => Object.values(rooms)),
        },
      },
      getActiveModel: () => null,
      notify: vi.fn(),
      log: vi.fn(),
    } as never);
    controller.conversations = Object.values(rooms) as never;
    controller.conversation = rooms["room-a"] as never;
    controller.activeId = "room-a";
    controller.attachments = [{ type: "text", content: "x", mediaType: "text/plain", name: "a.txt" }];
    await controller.open("room-b");
    expect(controller.attachments).toHaveLength(1);
    expect(controller.attachments[0]?.name).toBe("a.txt");
  });
});

describe("attachment content pure inspections (supporting BH-AGENT-16/18)", () => {
  it("rejects control-character binary-as-text and accepts clean UTF-8", () => {
    expect(inspectAttachmentContent(new Uint8Array([0, 1, 2, 3, 4]))).toBeNull();
    expect(inspectAttachmentContent(new TextEncoder().encode("hello"))).toMatchObject({ kind: "text" });
  });
});
