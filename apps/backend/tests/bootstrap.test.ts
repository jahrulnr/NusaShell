import { describe, expect, it, afterAll } from "vitest";
import { createContainer } from "../src/container.js";
import { WebSocket } from "ws";

describe("createContainer", () => {
  let container: ReturnType<typeof createContainer>;

  afterAll(async () => {
    await container.wsServer.stop();
  });

  it("wires all dependencies", () => {
    container = createContainer({ port: 9134 });

    expect(container.commandBus).toBeDefined();
    expect(container.queryBus).toBeDefined();
    expect(container.eventDispatcher).toBeDefined();
    expect(container.runtimeManager).toBeDefined();
    expect(container.router).toBeDefined();
    expect(container.wsServer).toBeDefined();
    expect(container.eventPublisher).toBeDefined();
  });

  it("uses the stamped app version for the system version query", async () => {
    container = createContainer({ port: 9137, appVersion: "0.3.0" });

    await expect(container.queryBus.execute({ kind: "system-version" })).resolves.toMatchObject({
      version: "0.3.0",
      name: "NusaShell",
    });
  });

  it("configures an OpenAI-compatible provider without requiring a default model", () => {
    container = createContainer({ port: 9138 });

    expect(() => container.configureAi({
      providerId: "openai-compatible",
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
    })).not.toThrow();
  });

  it("removes a configured provider from the live agent registry", async () => {
    container = createContainer({ port: 9139 });
    container.configureAi({
      providerId: "temporary-provider",
      baseUrl: "https://provider.example/v1",
      apiKey: "secret-key",
    });

    container.removeAi("temporary-provider");

    await expect(container.commandBus.execute({
      kind: "run-agent-turn",
      providerId: "temporary-provider",
      messages: [{ role: "user", content: "hello" }],
      pluginIds: [],
    })).rejects.toMatchObject({ code: "AGENT_PROVIDER_NOT_FOUND" });
  });

  it("does not expose the deterministic stub unless the environment configuration enables it", async () => {
    container = createContainer({ port: 9138 });
    await expect(container.commandBus.execute({
      kind: "run-agent-turn",
      messages: [{ role: "user", content: "hello" }],
      pluginIds: [],
    })).rejects.toMatchObject({ code: "AGENT_PROVIDER_NOT_FOUND" });
  });

  it("starts the WebSocket server and accepts connections", async () => {
    container = createContainer({ port: 9135 });
    await container.wsServer.start();

    await new Promise<void>((resolve, reject) => {
      const ws = new WebSocket("ws://127.0.0.1:9135");
      ws.on("open", () => {
        ws.close();
        resolve();
      });
      ws.on("error", reject);
      setTimeout(() => reject(new Error("timeout")), 5000);
    });
  });

  it("responds to plugin.list query", async () => {
    container = createContainer({ port: 9136 });
    await container.wsServer.start();

    await new Promise<void>((resolve, reject) => {
      const ws = new WebSocket("ws://127.0.0.1:9136");
      ws.on("open", () => {
        ws.send(JSON.stringify({
          kind: "request",
          id: "req_001",
          method: "plugin.list",
          payload: {},
        }));
      });
      ws.on("message", (data: Buffer) => {
        const response = JSON.parse(data.toString("utf-8"));
        expect(response.kind).toBe("response");
        expect(response.id).toBe("req_001");
        expect(response.ok).toBe(true);
        expect(response.result.plugins).toEqual([]);
        ws.close();
        resolve();
      });
      ws.on("error", reject);
      setTimeout(() => reject(new Error("timeout")), 5000);
    });
  });
});
