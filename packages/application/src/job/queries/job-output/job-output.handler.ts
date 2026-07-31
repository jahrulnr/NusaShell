import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { JobOutputQuery, JobOutputResult } from "./job-output.query.js";

export class JobOutputHandler implements QueryHandler<JobOutputQuery, JobOutputResult> {
  constructor(private readonly store: JobStorePort) {}
  async handle(query: JobOutputQuery): Promise<JobOutputResult> {
    return this.store.listOutputs(query.id, query.limit ?? 20);
  }
}
