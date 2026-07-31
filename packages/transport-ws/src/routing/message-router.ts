import type {
  CommandBus,
  QueryBus,
} from "@nusashell/application";
import type { LoggerPort } from "@nusashell/application";
import type { ResponseEnvelope } from "@nusashell/contracts";
import { validateIncomingMessage } from "../validation/incoming-message.validator.js";
import { mapToCommand } from "../mapping/command.mapper.js";
import { mapToQuery } from "../mapping/query.mapper.js";
import { mapSuccessResponse, mapErrorResponse } from "../mapping/response.mapper.js";
import { mapApplicationError } from "../mapping/error.mapper.js";
import { ProtocolError } from "../protocol/websocket-error.js";
import { ApplicationError } from "@nusashell/application";

export interface MessageRouterDeps {
  readonly commandBus: CommandBus;
  readonly queryBus: QueryBus;
  readonly logger?: LoggerPort;
}

export class MessageRouter {
  private closed = false;
  private readonly deps: MessageRouterDeps;

  constructor(deps: MessageRouterDeps) {
    this.deps = deps;
  }

  close(): void {
    this.closed = true;
  }

  get isClosed(): boolean {
    return this.closed;
  }

  async handle(raw: unknown): Promise<ResponseEnvelope> {
    const validation = validateIncomingMessage(raw);
    if (!validation.success) {
      const error = validation.error;
      return mapErrorResponse(error.requestId ?? "", error);
    }

    const request = validation.request;

    if (this.closed) {
      return mapErrorResponse(
        request.id,
        new ProtocolError("UNAVAILABLE", "Server is shutting down", request.id),
      );
    }

    try {
      const mapped = mapToCommand(request);

      if (mapped.kind === "command") {
        const result = await this.deps.commandBus.execute(mapped.command);
        if (mapped.command.kind === "run-agent-turn" && result && typeof result === "object" && "messages" in result) {
          const { messages: _messages, ...stripped } = result as Record<string, unknown>;
          return mapSuccessResponse(request.id, stripped);
        }
        return mapSuccessResponse(request.id, result);
      }

      const query = mapToQuery(request);
      if (query) {
        const result = await this.deps.queryBus.execute(query);
        return mapSuccessResponse(request.id, result);
      }

      return mapErrorResponse(
        request.id,
        new ProtocolError("INVALID_REQUEST", `Unknown method: ${request.method}`, request.id),
      );
    } catch (err) {
      if (err instanceof ApplicationError) {
        return mapErrorResponse(request.id, mapApplicationError(err, request.id));
      }
      if (err instanceof ProtocolError) {
        return mapErrorResponse(request.id, err);
      }
      this.deps.logger?.error("WS message router unhandled error method=%s requestId=%s: %s", request.method, request.id, err);
      return mapErrorResponse(
        request.id,
        new ProtocolError("INTERNAL_ERROR", String(err), request.id),
      );
    }
  }
}
