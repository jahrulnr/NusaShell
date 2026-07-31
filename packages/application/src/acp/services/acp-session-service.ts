import type { LoggerPort } from "../../plugin/ports/logger.port.js";
import type { EventDispatcher } from "../../events/event-dispatcher.js";
import { ApplicationError } from "../../errors/application-error.js";
import {
  createAcpTextDeltaEvent,
  createAcpThoughtDeltaEvent,
  createAcpToolCallEvent,
  createAcpToolCallUpdateEvent,
  createAcpPlanEvent,
  createAcpPermissionRequestEvent,
  createAcpAskRequestEvent,
  createAcpTurnEndEvent,
  createAcpSessionStateEvent,
} from "../events/index.js";
import type {
  AcpClientEvent,
  AcpClientPort,
  AcpClientSink,
  AcpContentBlock,
  AcpProviderDescriptor,
} from "../ports/acp-client.port.js";
import type { AcpPermissionService } from "./acp-permission-service.js";
import type { AcpAskBridgeService } from "./acp-ask-bridge-service.js";

export interface AcpSessionInfo {
  readonly conversationId: string;
  readonly sessionId: string;
  readonly provider: AcpProviderDescriptor;
  readonly workspace: string;
  readonly state: "idle" | "starting" | "running" | "error" | "cancelled";
  readonly traceId?: string | undefined;
}

interface AcpSession {
  readonly sessionId: string;
  provider: AcpProviderDescriptor;
  workspace: string;
  traceId: string | null;
  state: AcpSessionInfo["state"];
}

export interface AcpSessionServiceDeps {
  readonly client: AcpClientPort;
  readonly permissionService: AcpPermissionService;
  readonly askService: AcpAskBridgeService;
  readonly eventDispatcher: EventDispatcher;
  readonly logger?: LoggerPort;
}

export class AcpSessionService {
  private readonly sessions = new Map<string, AcpSession>();

  constructor(private readonly deps: AcpSessionServiceDeps) {}

  async startTurn(
    traceId: string,
    conversationId: string,
    workspace: string | undefined,
    provider: AcpProviderDescriptor,
    prompt: readonly AcpContentBlock[],
  ): Promise<void> {
    const cwd = workspace ?? process.cwd();
    const existing = this.sessions.get(conversationId);

    if (!existing || !sameProvider(existing.provider, provider) || existing.workspace !== cwd) {
      if (existing) {
        this.deps.logger?.info(`Restarting ACP session with changed provider or workspace for ${conversationId}`);
        await this.deps.client.closeSession(conversationId).catch((err) => {
          this.deps.logger?.warn(`Error closing previous ACP session for ${conversationId}: ${err}`);
        });
      }
      const sessionId = await this.deps.client.startSession(
        conversationId,
        provider,
        cwd,
        this.buildSink(conversationId, traceId),
      );
      this.sessions.set(conversationId, {
        sessionId,
        provider,
        workspace: cwd,
        traceId,
        state: "starting",
      });
      this.deps.eventDispatcher.publish(createAcpSessionStateEvent(traceId, conversationId, "starting"));
    } else {
      existing.traceId = traceId;
      existing.state = "starting";
    }

    const session = this.sessions.get(conversationId)!;
    session.traceId = traceId;
    session.state = "running";
    this.deps.eventDispatcher.publish(createAcpSessionStateEvent(traceId, conversationId, "running"));

    try {
      await this.deps.client.prompt(traceId, conversationId, prompt);
    } finally {
      this.finishTurn(conversationId, traceId);
    }
  }

  async cancelTurn(traceId: string, conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) {
      throw new ApplicationError("AGENT_TURN_CANCELLED", `No ACP session for conversation ${conversationId}`, { conversationId });
    }
    session.traceId = traceId;
    await this.deps.client.cancel(traceId, conversationId);
  }

  async getSessionInfo(conversationId: string): Promise<AcpSessionInfo | null> {
    const session = this.sessions.get(conversationId);
    if (!session) return null;
    return {
      conversationId,
      sessionId: session.sessionId,
      provider: session.provider,
      workspace: session.workspace,
      state: session.state,
      traceId: session.traceId ?? undefined,
    };
  }

  async closeSession(conversationId: string): Promise<void> {
    const session = this.sessions.get(conversationId);
    if (!session) return;
    this.sessions.delete(conversationId);
    await this.deps.client.closeSession(conversationId).catch((err) => {
      this.deps.logger?.warn(`Error closing ACP session for ${conversationId}: ${err}`);
    });
  }

  private finishTurn(conversationId: string, traceId: string): void {
    const session = this.sessions.get(conversationId);
    if (!session || session.traceId !== traceId) return;
    session.state = "idle";
    session.traceId = null;
    this.deps.askService.clearTurn(conversationId);
    this.deps.permissionService.clearTurn(conversationId);
  }

  private buildSink(conversationId: string, activeTraceId: string): AcpClientSink {
    const resolveTraceId = () => {
      const session = this.sessions.get(conversationId);
      return session?.traceId ?? activeTraceId;
    };

    return {
      publish: (event) => this.handleClientEvent(conversationId, resolveTraceId(), event),
      requestPermission: async (request) => {
        const traceId = resolveTraceId();
        this.deps.eventDispatcher.publish(
          createAcpPermissionRequestEvent(
            traceId,
            conversationId,
            request.requestId,
            request.toolTitle,
            request.detail,
            request.options,
          ),
        );
        return this.deps.permissionService.request(traceId, conversationId, request.requestId, request);
      },
      askQuestion: async (request) => {
        const traceId = resolveTraceId();
        this.deps.eventDispatcher.publish(
          createAcpAskRequestEvent(
            traceId,
            conversationId,
            request.requestId,
            request.question,
            request.options,
            request.multiSelect,
            request.allowFreeText,
          ),
        );
        return this.deps.askService.request(traceId, conversationId, request.requestId, request);
      },
    };
  }

  private handleClientEvent(conversationId: string, traceId: string, event: AcpClientEvent): void {
    switch (event.type) {
      case "acp.text_delta":
        this.deps.eventDispatcher.publish(createAcpTextDeltaEvent(traceId, conversationId, event.delta));
        break;
      case "acp.thought_delta":
        this.deps.eventDispatcher.publish(createAcpThoughtDeltaEvent(traceId, conversationId, event.delta));
        break;
      case "acp.tool_call":
        this.deps.eventDispatcher.publish(createAcpToolCallEvent(traceId, conversationId, event.call));
        break;
      case "acp.tool_call_update":
        this.deps.eventDispatcher.publish(createAcpToolCallUpdateEvent(traceId, conversationId, event.callId, event.status, event.summary));
        break;
      case "acp.plan":
        this.deps.eventDispatcher.publish(createAcpPlanEvent(traceId, conversationId, event.steps));
        break;
      case "acp.session_state":
        this.deps.eventDispatcher.publish(createAcpSessionStateEvent(traceId, event.conversationId, event.state));
        break;
      case "acp.turn_end":
        this.deps.eventDispatcher.publish(createAcpTurnEndEvent(traceId, conversationId, event.ok, event.error));
        this.finishTurn(conversationId, traceId);
        break;
    }
  }
}

function sameProvider(a: AcpProviderDescriptor, b: AcpProviderDescriptor): boolean {
  return (
    a.providerId === b.providerId &&
    a.command === b.command &&
    a.args.length === b.args.length &&
    a.args.every((arg, i) => arg === b.args[i]) &&
    a.authMethodId === b.authMethodId
  );
}
