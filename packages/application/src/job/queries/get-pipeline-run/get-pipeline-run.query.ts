import type { Query } from "../../../messaging/query.js";
import type { PipelineRun } from "../../pipeline-model.js";

export interface GetPipelineRunQuery extends Query {
  readonly kind: "get-pipeline-run";
  readonly runId: string;
  readonly includeBody?: boolean;
}

export interface GetPipelineRunResult {
  readonly run: PipelineRun | null;
}
