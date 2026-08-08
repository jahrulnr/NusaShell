import { randomUUID } from "node:crypto";
import { ApplicationError } from "../../errors/application-error.js";
import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { SubagentPort } from "../ports/subagent-port.js";
import { optionalString, requireString } from "./gateway-utils.js";
import { resolveAgentWorkspace } from "./resolve-agent-workspace.js";

const RETRYABLE_PATTERNS = [
  /rate.?limit/i,
  /usage.?limit/i,
  /quota/i,
  /429/,
  /503/,
  /502/,
  /temporarily unavailable/i,
  /connection refused/i,
  /econnrefused/i,
  /spawn/i,
  /enoent/i,
  /timeout/i,
  /timed out/i,
  /reset/i,
  /socket hang up/i,
];

function isRetryable(error: unknown): boolean {
  const message = error instanceof Error ? error.message : String(error);
  return RETRYABLE_PATTERNS.some((pattern) => pattern.test(message));
}

export async function execSubagent(
  port: SubagentPort | undefined,
  args: Readonly<Record<string, unknown>>,
  turnId: string,
  workspace: string | undefined,
  logger?: LoggerPort,
  parentConversationId?: string,
): Promise<unknown> {
  if (!port) {
    const message = "No ACP providers are connected";
    logger?.warn("Subagent rejected: subagent_not_configured");
    return {
      ok: false,
      code: "subagent_not_configured",
      error: message,
    };
  }
  const prompt = requireString(args.prompt, "prompt").trim();
  if (!prompt) throw new ApplicationError("AGENT_INVALID_INPUT", "prompt must not be empty");
  const title = optionalString(args.title) || undefined;
  const effectiveWorkspace = resolveAgentWorkspace(optionalString(args.workspace) || undefined, workspace);

  // Do not pass provider_id from the LLM to the resolver. The user's ACP
  // routing default (Settings → ACP Agents) is authoritative; letting the
  // LLM override it causes the subagent to use whichever provider the LLM
  // picks (often Cursor, since it's the first example in the tool description)
  // instead of the user's configured default (e.g. Gemini).
  const resolved = await port.resolve({ workspace: effectiveWorkspace });
  if (resolved.tryOrder.length === 0) {
    const message = "No ACP providers are connected. Connect one in Settings → ACP Agents.";
    logger?.warn("Subagent rejected: no_acp_provider");
    return {
      ok: false,
      code: "no_acp_provider",
      error: message,
      workspace: effectiveWorkspace,
    };
  }

  const runId = randomUUID();
  const conversationId = `subagent:${runId}`;
  const attempted: string[] = [];
  const failures: Array<{ providerId: string; error: string }> = [];
  // Host-prefix the absolute cwd so the ACP agent cannot invent a different path.
  // Role-frame the capability boundary so the subagent never simulates the
  // parent's MCP plugins, skills, or meta-tools — none of them exist in the
  // ACP process, and the parent brief may still reference them by mistake.
  const promptBlocks = [{
    type: "text" as const,
    text: `Working directory (cwd): ${effectiveWorkspace}\n\nYou are a subagent delegated by NusaShell's parent agent. You have only your own tools — the parent's MCP plugins, skills, and meta-tools do not exist here. If the task references a capability you do not have, say so in your final message instead of simulating it.\n\n${prompt}`,
  }];

  logger?.info("Subagent workspace runId=%s cwd=%s", runId, effectiveWorkspace);

  for (const providerId of resolved.tryOrder) {
    const candidate = resolved.candidates.get(providerId);
    if (!candidate) continue;
    attempted.push(providerId);
    try {
      const result = await port.run({
        runId,
        conversationId,
        ...(parentConversationId ? { parentConversationId } : {}),
        ...(turnId ? { parentTraceId: turnId } : {}),
        providerId,
        workspace: effectiveWorkspace,
        prompt: promptBlocks,
        ...(title ? { title } : {}),
        ...(candidate.preferredConfig ? { preferredConfig: candidate.preferredConfig } : {}),
      });
      if (result.ok) {
        return {
          ok: true,
          runId,
          providerId: result.providerId,
          workspace: effectiveWorkspace,
          ...(attempted.length > 1 ? { attempted: attempted.slice(0, -1) } : {}),
          ...(title ? { title } : {}),
          summary: result.summary,
          ...(result.configWarnings?.length ? { configWarnings: result.configWarnings } : {}),
        };
      }
      const errorMsg = result.error ?? "Subagent turn failed";
      failures.push({ providerId, error: errorMsg });
      if (!isRetryable(result.error)) {
        return {
          ok: false,
          runId,
          providerId: result.providerId,
          workspace: effectiveWorkspace,
          ...(attempted.length > 1 ? { attempted: attempted.slice(0, -1) } : {}),
          ...(title ? { title } : {}),
          error: errorMsg,
          ...(failures.length > 1 ? { failures: failures.slice(0, -1) } : {}),
        };
      }
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : String(error);
      failures.push({ providerId, error: errorMsg });
      if (!isRetryable(error)) {
        throw error;
      }
    }
  }

  return {
    ok: false,
    runId,
    workspace: effectiveWorkspace,
    ...(attempted.length > 0 ? { providerId: attempted[attempted.length - 1] } : {}),
    attempted,
    ...(title ? { title } : {}),
    error: `All ACP providers failed: ${failures.map((f) => `${f.providerId} (${f.error})`).join(", ")}`,
    failures,
  };
}
