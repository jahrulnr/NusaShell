import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { AcpProviderStore } from "../src/main/acp-provider-store.js";

async function makeStore(providers: unknown, routing?: unknown) {
  const directory = await mkdtemp(join(tmpdir(), "nusashell-acp-"));
  const providersPath = join(directory, "acp-providers.json");
  const routingPath = join(directory, "acp-routing.json");
  await writeFile(providersPath, JSON.stringify(providers, null, 2));
  if (routing !== undefined) {
    await writeFile(routingPath, JSON.stringify(routing, null, 2));
  }
  return new AcpProviderStore(providersPath, routingPath);
}

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

  it("puts an override first when that provider is connected", async () => {
    const store = await makeStore([
      { providerId: "codex", enabled: true, authStatus: "connected" },
      { providerId: "cursor", enabled: true, authStatus: "connected" },
    ]);

    await expect(store.resolveTryOrder("codex")).resolves.toEqual(["codex", "cursor"]);
  });
});
