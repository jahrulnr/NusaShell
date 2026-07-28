import { describe, expect, it } from "vitest";
import { MessageRouter } from "../src/routing/message-router.js";
import { CommandBus, QueryBus, ApplicationError } from "@nusashell/application";

describe("MessageRouter", () => {
  it("routes plugin.list query and returns success response", async () => {
    const queryBus = new QueryBus();
    queryBus.register("list-plugins", {
      handle: async () => ({
        plugins: [
          { pluginId: "com.example.notes", name: "Notes", version: "1.0.0", icon: "📝", state: "idle", enabled: true },
        ],
      }),
    } as never);

    const router = new MessageRouter({ commandBus: new CommandBus(), queryBus });
    const response = await router.handle({
      kind: "request",
      id: "req_001",
      method: "plugin.list",
      payload: {},
    });

    expect(response.kind).toBe("response");
    expect(response.id).toBe("req_001");
    expect(response.ok).toBe(true);
  });

  it("returns error for invalid request", async () => {
    const router = new MessageRouter({
      commandBus: new CommandBus(),
      queryBus: new QueryBus(),
    });

    const response = await router.handle("not json");

    expect(response.ok).toBe(false);
    if (!response.ok) {
      expect(response.error.code).toBe("INVALID_REQUEST");
    }
  });

  it("returns error for unknown method", async () => {
    const router = new MessageRouter({
      commandBus: new CommandBus(),
      queryBus: new QueryBus(),
    });

    const response = await router.handle({
      kind: "request",
      id: "req_002",
      method: "plugin.unknown",
      payload: {},
    });

    expect(response.ok).toBe(false);
  });

  it("maps ApplicationError to error response", async () => {
    const commandBus = new CommandBus();
    commandBus.register("start-plugin", {
      handle: async () => {
        throw new ApplicationError("PLUGIN_NOT_FOUND", "Plugin not found");
      },
    } as never);

    const router = new MessageRouter({ commandBus, queryBus: new QueryBus() });

    const response = await router.handle({
      kind: "request",
      id: "req_003",
      method: "plugin.start",
      payload: { pluginId: "com.unknown" },
    });

    expect(response.ok).toBe(false);
    if (!response.ok) {
      expect(response.error.code).toBe("PLUGIN_NOT_FOUND");
    }
  });
});
