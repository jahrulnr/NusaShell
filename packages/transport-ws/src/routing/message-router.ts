import type {
  CommandBus,
  QueryBus,
} from "@nusashell/application";
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
}

export class MessageRouter {
  constructor(private readonly deps: MessageRouterDeps) {}

  async handle(raw: unknown): Promise<ResponseEnvelope> {
    const validation = validateIncomingMessage(raw);
    if (!validation.success) {
      const error = validation.error;
      return mapErrorResponse(error.requestId ?? "", error);
    }

    const request = validation.request;

    try {
      const mapped = mapToCommand(request);

      if (mapped.kind === "command") {
        const result = await this.deps.commandBus.execute(mapped.command);
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
      return mapErrorResponse(
        request.id,
        new ProtocolError("INTERNAL_ERROR", String(err), request.id),
      );
    }
  }
}
