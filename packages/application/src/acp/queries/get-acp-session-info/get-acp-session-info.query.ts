import type { Query } from "../../../messaging/query.js";
import type { AcpSessionInfo } from "../../services/acp-session-service.js";

export interface GetAcpSessionInfoQuery extends Query {
  readonly kind: "get-acp-session-info";
  readonly conversationId: string;
}

export type GetAcpSessionInfoResult = AcpSessionInfo | null;
