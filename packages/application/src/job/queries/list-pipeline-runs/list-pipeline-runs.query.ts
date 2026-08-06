import type { Query } from "../../../messaging/query.js";
import type { PipelineRun } from "../../pipeline-model.js";

export interface ListPipelineRunsQuery extends Query {
  readonly kind: "list-pipeline-runs";
  readonly pipelineId: string;
  readonly limit?: number;
  /** When false (default), strip large step output previews from the payload. */
  readonly includeBody?: boolean;
}

export interface ListPipelineRunsResult {
  readonly runs: readonly PipelineRun[];
}
