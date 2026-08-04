import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { AcpProviderStore } from "../src/main/acp-provider-store.js";

async function makeStore(providers: unknown, routing?: unknown, logger?: { info: (msg: string, ...args: unknown[]) => void }) {
  const directory = await mkdtemp(join(tmpdir(), "nusashell-acp-"));
  const providersPath = join(directory, "acp-providers.json");
  const routingPath = join(directory, "acp-routing.json");
  await writeFile(providersPath, JSON.stringify(providers, null, 2));
  if (routing !== undefined) {
    await writeFile(routingPath, JSON.stringify(routing, null, 2));
  }
  return new AcpProviderStore(providersPath, routingPath, logger);
}

describe("AcpProviderStore", () => {
  it("lists Devin as an unverified built-in provider with the devin acp command", async () => {
    const store = await makeStore([]);
    const devin = (await store.list()).find((provider) => provider.manifest.id === "devin");

    expect(devin?.manifest).toMatchObject({
      displayName: "Devin",
      monogram: "DV",
      command: "devin",
      args: ["acp"],
      authMethodIds: ["devin-browser"],
      preferredConfig: { mode: "bypass" },
      unverified: true,
    });
  });

  it("publishes provider-specific ACP mode defaults", async () => {
    const store = await makeStore([]);
    const providers = await store.list();

    expect(providers.find((provider) => provider.manifest.id === "cursor")?.config.preferredConfig).toEqual({ mode: "agent" });
    expect(providers.find((provider) => provider.manifest.id === "codex")?.config.preferredConfig).toEqual({ mode: "agent-full-access" });
    expect(providers.find((provider) => provider.manifest.id === "devin")?.config.preferredConfig).toEqual({ mode: "bypass" });
  });

  it("allows a saved Devin command override without changing the manifest identity", async () => {
    const store = await makeStore([]);
    const updated = await store.save({ providerId: "devin", enabled: true, command: "/opt/devin", args: ["acp", "--local"] });
    const devin = updated.find((provider) => provider.manifest.id === "devin");

    expect(devin?.manifest.id).toBe("devin");
    expect(devin?.config).toMatchObject({ enabled: true, command: "/opt/devin", args: ["acp", "--local"] });
  });
});

describe("AcpProviderStore.resolveTryOrder", () => {
  it("falls back to connected enabled providers when routing is missing", async () => {
    const store = await makeStore([
      { providerId: "codex", enabled: true, authStatus: "connected", authCheckedAt: "2026-08-01T00:00:00.000Z" },
      { providerId: "cursor", enabled: true, authStatus: "connected", authCheckedAt: "2026-08-01T00:00:00.000Z" },
    ]);

    await expect(store.resolveTryOrder()).resolves.toEqual(["cursor", "codex"]);
  });

  it("prefers explicit default + fallback, then appends remaining connected", async () => {
    const store = await makeStore(
      [
        { providerId: "codex", enabled: true, authStatus: "connected" },
        { providerId: "cursor", enabled: true, authStatus: "connected" },
      ],
      { defaultProviderId: "codex", fallbackProviderIds: [] },
    );

    await expect(store.resolveTryOrder()).resolves.toEqual(["codex", "cursor"]);
  });

  it("skips disabled providers even when authStatus is connected", async () => {
    const store = await makeStore([
      { providerId: "codex", enabled: false, authStatus: "connected" },
      { providerId: "cursor", enabled: true, authStatus: "connected" },
    ]);

    await expect(store.resolveTryOrder()).resolves.toEqual(["cursor"]);
  });

  it("returns the routing try-order as-is (Settings authoritative)", async () => {
    const store = await makeStore([
      { providerId: "codex", enabled: true, authStatus: "connected" },
      { providerId: "cursor", enabled: true, authStatus: "connected" },
    ]);

    await expect(store.resolveTryOrder()).resolves.toEqual(["cursor", "codex"]);
  });
});

describe("AcpProviderStore.resolveTryOrder logging", () => {
  it("logs info with tryOrder list", async () => {
    const info = vi.fn();
    const store = await makeStore(
      [
        { providerId: "gemini", enabled: true, authStatus: "connected" },
        { providerId: "cursor", enabled: true, authStatus: "connected" },
      ],
      { defaultProviderId: "gemini", fallbackProviderIds: ["cursor"] },
      { info },
    );

    await store.resolveTryOrder();

    expect(info).toHaveBeenCalledTimes(1);
    const msg = info.mock.calls[0][0];
    expect(msg).toContain("acp.resolveTryOrder");
    expect(msg).toContain("[gemini,cursor]");
  });
});
