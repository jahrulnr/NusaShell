import type { QueryHandler } from "../../../messaging/query-handler.js";
import { AcpSessionService } from "../../services/acp-session-service.js";
import type { GetAcpSessionInfoQuery, GetAcpSessionInfoResult } from "./get-acp-session-info.query.js";

export class GetAcpSessionInfoHandler implements QueryHandler<GetAcpSessionInfoQuery, GetAcpSessionInfoResult> {
  constructor(private readonly sessionService: AcpSessionService) {}

  async handle(query: GetAcpSessionInfoQuery): Promise<GetAcpSessionInfoResult> {
    return this.sessionService.getSessionInfo(query.conversationId);
  }
}
