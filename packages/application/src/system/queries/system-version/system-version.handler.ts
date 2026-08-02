import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { SystemVersionQuery } from "./system-version.query.js";
import type { SystemVersionResult } from "./system-version.result.js";

export class SystemVersionHandler
  implements QueryHandler<SystemVersionQuery, SystemVersionResult>
{
  constructor(
    private readonly version: string = "0.0.0",
    private readonly name: string = "NusaShell",
  ) {}

  async handle(_query: SystemVersionQuery): Promise<SystemVersionResult> {
    return { version: this.version, name: this.name };
  }
}
