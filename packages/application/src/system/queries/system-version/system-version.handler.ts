import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { SystemVersionQuery } from "./system-version.query.js";
import type { SystemVersionResult } from "./system-version.result.js";

export class SystemVersionHandler
  implements QueryHandler<SystemVersionQuery, SystemVersionResult>
{
  async handle(_query: SystemVersionQuery): Promise<SystemVersionResult> {
    return { version: "0.0.9", name: "NusaShell" };
  }
}
