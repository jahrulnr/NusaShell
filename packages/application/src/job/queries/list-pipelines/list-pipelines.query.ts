import type { Query } from "../../../messaging/query.js";
import type { Pipeline } from "../../pipeline-model.js";

export interface ListPipelinesQuery extends Query {
  readonly kind: "list-pipelines";
}

export interface ListPipelinesResult {
  readonly pipelines: readonly Pipeline[];
}
