import type { Query } from "../../../messaging/query.js";
import type { Job } from "../../job-model.js";

export interface ListJobsQuery extends Query {
  readonly kind: "list-jobs";
}

export type ListJobsResult = readonly Job[];
