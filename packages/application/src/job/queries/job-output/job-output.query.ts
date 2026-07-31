import type { Query } from "../../../messaging/query.js";
import type { JobOutputEntry } from "../../job-model.js";

export interface JobOutputQuery extends Query {
  readonly kind: "job-output";
  readonly id: string;
  readonly limit?: number;
}

export type JobOutputResult = readonly JobOutputEntry[];
