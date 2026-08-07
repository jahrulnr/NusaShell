import { describe, expect, it, vi } from "vitest";

const { sendRequest, subscribeAgentTurnEvents } = vi.hoisted(() => ({
  sendRequest: vi.fn().mockResolvedValue({ text: "done" }),
  subscribeAgentTurnEvents: vi.fn().mockReturnValue({ disposers: [], lifecycleDisposers: [] }),
}));

vi.mock("../src/renderer/ws-client.js", () => ({ sendRequest }));
vi.mock("../src/renderer/turn-event-helper.js", () => ({ subscribeAgentTurnEvents }));

import { runAgentTurn } from "../src/renderer/agent-api.js";

describe("runAgentTurn IPC lifetime", () => {
  it("does not impose a wall-clock timeout on long-running agent turns", async () => {
    await runAgentTurn(
      [{ role: "user", content: "work for as long as needed" }],
      {},
      {
        models: [{ key: "provider:model", providerId: "provider", id: "model" }],
        activeModelKey: "provider:model",
        effort: "auto",
        userPrompt: "",
      },
    );

    expect(sendRequest).toHaveBeenCalledWith(
      "agent.run",
      expect.objectContaining({ model: "model" }),
      0,
    );
  });
});

describe("runAgentTurn per-conversation model (ticket #38)", () => {
  it("uses the modelKey+effort threaded from the caller over the global active model", async () => {
    await runAgentTurn(
      [{ role: "user", content: "hi" }],
      { modelKey: "room/gpt", effort: "high" },
      {
        models: [
          { key: "global/claude", providerId: "p1", id: "claude-3" },
          { key: "room/gpt", providerId: "p2", id: "gpt-5" },
        ],
        activeModelKey: "global/claude",
        effort: "auto",
        userPrompt: "",
      },
    );

    expect(sendRequest).toHaveBeenCalledWith(
      "agent.run",
      expect.objectContaining({ model: "gpt-5", providerId: "p2", effort: "high" }),
      0,
    );
  });

  it("falls back to the global active model when no caller modelKey is provided", async () => {
    await runAgentTurn(
      [{ role: "user", content: "hi" }],
      {},
      {
        models: [{ key: "global/claude", providerId: "p1", id: "claude-3" }],
        activeModelKey: "global/claude",
        effort: "auto",
        userPrompt: "",
      },
    );

    expect(sendRequest).toHaveBeenCalledWith(
      "agent.run",
      expect.objectContaining({ model: "claude-3", providerId: "p1", effort: "auto" }),
      0,
    );
  });

  it("throws when the threaded room modelKey is not in the catalog", async () => {
    await expect(runAgentTurn(
      [{ role: "user", content: "hi" }],
      { modelKey: "room/missing" },
      {
        models: [{ key: "global/claude", providerId: "p1", id: "claude-3" }],
        activeModelKey: "global/claude",
        effort: "auto",
        userPrompt: "",
      },
    )).rejects.toThrow("Choose an imported AI model");
  });
});
