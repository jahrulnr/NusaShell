import { describe, expect, it } from "vitest";
import { ProbeAcpProviderHandler } from "../src/acp/commands/probe-acp-provider/probe-acp-provider.handler.js";
import type { AcpClientPort, AcpClientSink, AcpProviderDescriptor } from "../src/acp/ports/acp-client.port.js";
import { ApplicationError } from "../src/errors/application-error.js";

class FakeAcpClient implements AcpClientPort {
  startSessionCalls: { conversationId: string; provider: AcpProviderDescriptor; cwd: string }[] = [];
  closeSessionCalls: string[] = [];
  getConfigOptionsCalls: string[] = [];
  startShouldThrow: Error | null = null;

  async startSession(conversationId: string, provider: AcpProviderDescriptor, cwd: string, _sink: AcpClientSink): Promise<string> {
    this.startSessionCalls.push({ conversationId, provider, cwd });
    if (this.startShouldThrow) throw this.startShouldThrow;
    return "probe-session-id";
  }
  async prompt(): Promise<void> {}
  async cancel(): Promise<void> {}
  async closeSession(conversationId: string): Promise<void> { this.closeSessionCalls.push(conversationId); }
  getConfigOptions(): [] { return []; }
  async setConfigOption(): Promise<[]> { return []; }
}

const baseProvider = {
  providerId: "codex",
  command: "codex-acp",
  args: [] as readonly string[],
  env: { NO_BROWSER: "1" },
};

describe("ProbeAcpProviderHandler", () => {
  it("returns ok:true and closes the session when startSession succeeds", async () => {
    const client = new FakeAcpClient();
    const handler = new ProbeAcpProviderHandler(client);
    const result = await handler.handle({ kind: "probe-acp-provider", provider: baseProvider });
    expect(result.ok).toBe(true);
    expect(client.startSessionCalls).toHaveLength(1);
    expect(client.startSessionCalls[0]!.provider.env).toEqual({ NO_BROWSER: "1" });
    expect(client.closeSessionCalls).toHaveLength(1);
  });

  it("returns ok:false with error message when startSession fails (hard fail)", async () => {
    const client = new FakeAcpClient();
    client.startShouldThrow = new ApplicationError("AGENT_PROVIDER_FAILED", "spawn failed");
    const handler = new ProbeAcpProviderHandler(client);
    const result = await handler.handle({ kind: "probe-acp-provider", provider: baseProvider });
    expect(result.ok).toBe(false);
    expect(result.error).toContain("spawn failed");
  });

  it("passes authMethodId through to the provider descriptor", async () => {
    const client = new FakeAcpClient();
    const handler = new ProbeAcpProviderHandler(client);
    await handler.handle({ kind: "probe-acp-provider", provider: { ...baseProvider, authMethodId: "api-key" } });
    expect(client.startSessionCalls[0]!.provider.authMethodId).toBe("api-key");
  });

  it("uses a throwaway conversationId and tmpdir cwd", async () => {
    const client = new FakeAcpClient();
    const handler = new ProbeAcpProviderHandler(client);
    await handler.handle({ kind: "probe-acp-provider", provider: baseProvider });
    expect(client.startSessionCalls[0]!.conversationId).toMatch(/^probe-codex-/);
    expect(client.startSessionCalls[0]!.cwd).toBeTruthy();
  });
});
