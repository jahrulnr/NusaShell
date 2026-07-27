import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { SystemPingQuery } from "./system-ping.query.js";
import type { SystemPingResult } from "./system-ping.result.js";

export class SystemPingHandler
  implements QueryHandler<SystemPingQuery, SystemPingResult>
{
  async handle(_query: SystemPingQuery): Promise<SystemPingResult> {
    return { pong: true, timestamp: new Date().toISOString() };
  }
}
