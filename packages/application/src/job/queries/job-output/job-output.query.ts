import type { Query } from "../../../messaging/query.js";
import type { JobOutputEntry } from "../../job-model.js";

export interface JobOutputQuery extends Query {
  readonly kind: "job-output";
  readonly id: string;
  readonly limit?: number;
  /** When true, include the full markdown body for each entry (capped). */
  readonly includeBody?: boolean;
}

export interface JobOutputItem extends JobOutputEntry {
  readonly body?: string;
}

export interface JobOutputResult {
  readonly outputs: readonly JobOutputItem[];
}
