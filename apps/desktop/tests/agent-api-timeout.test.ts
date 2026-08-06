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
