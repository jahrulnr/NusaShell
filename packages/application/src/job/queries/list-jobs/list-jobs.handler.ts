import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { ListJobsQuery, ListJobsResult } from "./list-jobs.query.js";

export class ListJobsHandler implements QueryHandler<ListJobsQuery, ListJobsResult> {
  constructor(private readonly store: JobStorePort) {}
  async handle(): Promise<ListJobsResult> {
    const jobs = await this.store.list();
    return { jobs };
  }
}
