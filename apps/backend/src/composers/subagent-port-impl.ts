import fs from "node:fs";
import type {
  AcpSessionService,
  SubagentPort,
  SubagentResolveRequest,
  SubagentResolveResult,
  SubagentRunRequest,
  SubagentRunResult,
  SubagentRoutingInfo,
  AcpProviderResolverPort,
  EventDispatcher,
  EventHandler,
  ApplicationEvent,
} from "@nusashell/application";
import { createSubagentRunStartedEvent, createSubagentRunEndedEvent } from "@nusashell/application";
import type { Logger } from "@nusashell/infrastructure";

/**
 * Bridges the application-layer SubagentPort to the ACP provider resolver
 * (connected state + routing) and AcpSessionService (turn execution).
 */
export class SubagentPortImpl implements SubagentPort {
  constructor(
    private readonly resolver: AcpProviderResolverPort,
    private readonly sessionService: AcpSessionService,
    private readonly eventDispatcher: EventDispatcher,
    private readonly logger?: Logger,
  ) {}

  async resolve(_request: SubagentResolveRequest): Promise<SubagentResolveResult> {
    return this.resolver.resolve();
  }

  async getRoutingInfo(): Promise<SubagentRoutingInfo | null> {
    try {
      const resolved = await this.resolver.resolve();
      if (resolved.tryOrder.length === 0) return null;
      return {
        availableSubagents: resolved.tryOrder.join(", "),
        defaultSubagent: resolved.tryOrder[0] ?? "",
      };
    } catch {
      return null;
    }
  }

  async run(request: SubagentRunRequest): Promise<SubagentRunResult> {
    const resolved = await this.resolver.resolve();
    const candidate = resolved.candidates.get(request.providerId);
    if (!candidate) {
      return { ok: false, providerId: request.providerId, summary: "", error: `ACP provider "${request.providerId}" is not connected` };
    }

    this.logger?.info("Subagent run start runId=%s provider=%s workspace=%s", request.runId, request.providerId, request.workspace);

    const promptText = request.prompt.map((block) => block.type === "text" ? block.text : "").join("");
    this.eventDispatcher.publish(createSubagentRunStartedEvent(
      request.runId,
      request.conversationId,
      request.providerId,
      promptText,
      {
        ...(request.title ? { title: request.title } : {}),
        ...(request.parentConversationId ? { parentConversationId: request.parentConversationId } : {}),
        ...(request.parentTraceId ? { parentTraceId: request.parentTraceId } : {}),
      },
    ));

    const textChunks: string[] = [];
    let breakBeforeNextText = false;
    const textHandler: EventHandler = {
      handle: (event: ApplicationEvent) => {
        if (event.type === "acp.tool_call" || event.type === "acp.tool_call_update" || event.type === "acp.thought_delta") {
          const related = event as ApplicationEvent & { aggregateId?: string; traceId?: string };
          const traceId = related.traceId ?? related.aggregateId;
          if (traceId === request.runId) breakBeforeNextText = true;
          return;
        }
        if (event.type !== "acp.text_delta") return;
        const deltaEvent = event as ApplicationEvent & { aggregateId?: string; traceId?: string; delta?: string };
        const traceId = deltaEvent.traceId ?? deltaEvent.aggregateId;
        if (traceId !== request.runId) return;
        if (typeof deltaEvent.delta === "string" && deltaEvent.delta) {
          if (breakBeforeNextText && textChunks.length > 0) {
            textChunks.push("\n\n");
            breakBeforeNextText = false;
          }
          textChunks.push(deltaEvent.delta);
        }
      },
    };
    this.eventDispatcher.on("acp.text_delta", textHandler);
    this.eventDispatcher.on("acp.tool_call", textHandler);
    this.eventDispatcher.on("acp.tool_call_update", textHandler);
    this.eventDispatcher.on("acp.thought_delta", textHandler);

    try {
      fs.mkdirSync(request.workspace, { recursive: true });

      // Ensure the ACP session exists. preferredConfig is applied inside
      // ensureSession/startTurn — no duplicate apply here. Config failures
      // are collected and surfaced to the caller via configWarnings.
      await this.sessionService.ensureSession(
        request.conversationId,
        request.workspace,
        candidate.descriptor,
      );

      await this.sessionService.startTurn(
        request.runId,
        request.conversationId,
        request.workspace,
        candidate.descriptor,
        request.prompt,
      );

      // Collect any non-fatal config warnings from the session.
      const sessionInfo = await this.sessionService.getSessionInfo(request.conversationId);
      const configWarnings = sessionInfo?.configWarnings ?? [];

      const summary = textChunks.join("").trim() || "Subagent turn completed.";
      this.eventDispatcher.publish(createSubagentRunEndedEvent(request.runId, request.conversationId, request.providerId, true, { summary }));
      return {
        ok: true,
        providerId: request.providerId,
        summary,
        ...(configWarnings.length > 0 ? { configWarnings } : {}),
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error);
      this.logger?.warn("Subagent run failed runId=%s provider=%s: %s", request.runId, request.providerId, message);
      this.eventDispatcher.publish(createSubagentRunEndedEvent(request.runId, request.conversationId, request.providerId, false, { error: message }));
      return {
        ok: false,
        providerId: request.providerId,
        summary: textChunks.join("").trim(),
        error: message,
      };
    } finally {
      this.eventDispatcher.off("acp.text_delta", textHandler);
      this.eventDispatcher.off("acp.tool_call", textHandler);
      this.eventDispatcher.off("acp.tool_call_update", textHandler);
      this.eventDispatcher.off("acp.thought_delta", textHandler);
    }
  }

  async cancel(runId: string, conversationId: string): Promise<void> {
    try {
      await this.sessionService.cancelTurn(runId, conversationId);
    } catch (error) {
      this.logger?.warn("Subagent cancel failed runId=%s: %s", runId, error instanceof Error ? error.message : String(error));
    }
  }
}
