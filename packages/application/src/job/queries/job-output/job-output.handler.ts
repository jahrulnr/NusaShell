import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { JobStorePort } from "../../ports/job-store.port.js";
import type { JobFsPort } from "../../ports/job-fs.port.js";
import type { JobOutputQuery, JobOutputResult, JobOutputItem } from "./job-output.query.js";

const MAX_BODY_CHARS = 100_000;

export class JobOutputHandler implements QueryHandler<JobOutputQuery, JobOutputResult> {
  constructor(
    private readonly store: JobStorePort,
    private readonly jobFs?: JobFsPort,
  ) {}

  async handle(query: JobOutputQuery): Promise<JobOutputResult> {
    const entries = await this.store.listOutputs(query.id, query.limit ?? 20);
    if (!query.includeBody || !this.jobFs) {
      return { outputs: entries };
    }
    const items: JobOutputItem[] = [];
    for (const entry of entries) {
      let body: string | undefined;
      try {
        const raw = await this.jobFs.readJobOutput(entry.path);
        if (raw !== null) body = raw.slice(0, MAX_BODY_CHARS);
      } catch {
        // best-effort: leave body undefined on read failure
      }
      items.push({ ...entry, ...(body !== undefined ? { body } : {}) });
    }
    return { outputs: items };
  }
}
