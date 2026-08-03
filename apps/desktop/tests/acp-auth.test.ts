import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";
import { AcpProviderStore } from "../src/main/acp-provider-store.js";
import { probeAcpProviderAuth, refreshAcpAuthStatuses } from "../src/main/acp-auth.js";

async function makeStore(providers: unknown = []) {
  const directory = await mkdtemp(join(tmpdir(), "nusashell-acp-auth-"));
  const providersPath = join(directory, "acp-providers.json");
  const routingPath = join(directory, "acp-routing.json");
  await writeFile(providersPath, JSON.stringify(providers, null, 2));
  return new AcpProviderStore(providersPath, routingPath);
}

describe("probeAcpProviderAuth", () => {
  it("marks connected from file auth without calling authenticate", async () => {
    const store = await makeStore([
      { providerId: "cursor", enabled: true },
    ]);
    const execute = vi.fn(async (command: { provider: { authMethodId?: string } }) => {
      expect(command.provider.authMethodId).toBeUndefined();
      return { ok: true };
    });

    const updated = await probeAcpProviderAuth(store, { execute }, "cursor", { interactive: true });
    expect(updated?.config.authStatus).toBe("connected");
    expect(execute).toHaveBeenCalledTimes(1);
  });

  it("retries with interactive auth only when file auth fails and interactive=true", async () => {
    const store = await makeStore([
      { providerId: "cursor", enabled: true },
    ]);
    const execute = vi.fn(async (command: { provider: { authMethodId?: string } }) => {
      if (!command.provider.authMethodId) return { ok: false, error: "not logged in" };
      expect(command.provider.authMethodId).toBe("cursor_login");
      return { ok: true };
    });

    const updated = await probeAcpProviderAuth(store, { execute }, "cursor", { interactive: true });
    expect(updated?.config.authStatus).toBe("connected");
    expect(execute).toHaveBeenCalledTimes(2);
  });

  it("does not open interactive auth or downgrade on silent failure", async () => {
    const store = await makeStore([
      { providerId: "cursor", enabled: true, authStatus: "needs-auth", authError: "old" },
    ]);
    const execute = vi.fn(async () => ({ ok: false, error: "still broken" }));

    const updated = await probeAcpProviderAuth(store, { execute }, "cursor", { interactive: false });
    expect(execute).toHaveBeenCalledTimes(1);
    expect(updated?.config.authStatus).toBe("needs-auth");
    expect(updated?.config.authError).toBe("old");
  });
});

describe("refreshAcpAuthStatuses", () => {
  it("silently reconnects enabled providers that are not connected", async () => {
    const store = await makeStore([
      { providerId: "cursor", enabled: true },
      { providerId: "codex", enabled: true, authStatus: "connected" },
    ]);
    const execute = vi.fn(async (command: { provider: { providerId: string; authMethodId?: string } }) => {
      expect(command.provider.authMethodId).toBeUndefined();
      return { ok: true };
    });
    const log = vi.fn();

    await refreshAcpAuthStatuses(store, { execute }, log);
    expect(execute).toHaveBeenCalledTimes(1);
    expect(execute.mock.calls[0]![0].provider.providerId).toBe("cursor");
    const cursor = await store.getEffective("cursor");
    expect(cursor?.config.authStatus).toBe("connected");
    expect(log).toHaveBeenCalledWith(expect.stringContaining("cursor"));
  });
});

describe("Cursor manifest auth defaults", () => {
  it("does not default Cursor to cursor_login", async () => {
    const store = await makeStore([]);
    const cursor = await store.getEffective("cursor");
    expect(cursor?.manifest.authMethodId).toBeUndefined();
    expect(cursor?.manifest.authMethodIds).toContain("cursor_login");
    expect(cursor?.config.authMethodId).toBeUndefined();
  });
});
