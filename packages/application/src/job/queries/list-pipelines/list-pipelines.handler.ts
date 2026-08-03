import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { ListPipelinesQuery, ListPipelinesResult } from "./list-pipelines.query.js";

export class ListPipelinesHandler implements QueryHandler<ListPipelinesQuery, ListPipelinesResult> {
  constructor(private readonly store: PipelineStorePort) {}
  async handle(): Promise<ListPipelinesResult> {
    const pipelines = await this.store.list();
    return { pipelines };
  }
}
