import type { AgentProvider, AgentProviderRequest, AgentProviderResult } from "@nusashell/application";

/** Offline-safe provider used by local development and deterministic tests. */
export class StaticAgentProvider implements AgentProvider {
  readonly id = "stub";

  async complete(request: AgentProviderRequest): Promise<AgentProviderResult> {
    const lastUserMessage = [...request.messages].reverse().find((message) => message.role === "user");
    return { text: `(stub) received: ${lastUserMessage?.content ?? ""}`, model: "stub" };
  }
}
