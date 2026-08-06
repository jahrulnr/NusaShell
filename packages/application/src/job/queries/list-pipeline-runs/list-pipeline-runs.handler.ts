import { ApplicationError } from "../../../errors/application-error.js";
import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PipelineRun, PipelineStepRun } from "../../pipeline-model.js";
import {
  boundPipelineText,
  DEFAULT_PIPELINE_LIST_SUMMARY_MAX_CHARS,
} from "../../pipeline-output.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { ListPipelineRunsQuery, ListPipelineRunsResult } from "./list-pipeline-runs.query.js";

export class ListPipelineRunsHandler implements QueryHandler<ListPipelineRunsQuery, ListPipelineRunsResult> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(query: ListPipelineRunsQuery): Promise<ListPipelineRunsResult> {
    const pipeline = await this.store.get(query.pipelineId);
    if (!pipeline) {
      throw new ApplicationError("PIPELINE_NOT_FOUND", `Pipeline not found: ${query.pipelineId}`);
    }
    const limit = query.limit ?? 20;
    const runs = await this.store.listRuns(query.pipelineId, limit);
    const includeBody = query.includeBody === true;
    return {
      runs: runs.map((run) => (includeBody ? run : compactRun(run))),
    };
  }
}

function compactRun(run: PipelineRun): PipelineRun {
  return {
    ...run,
    stepRuns: run.stepRuns.map(compactStepRun),
  };
}

function compactStepRun(step: PipelineStepRun): PipelineStepRun {
  const summary = step.summary
    ? boundPipelineText(step.summary, DEFAULT_PIPELINE_LIST_SUMMARY_MAX_CHARS).text
    : undefined;
  const error = step.error
    ? boundPipelineText(step.error, DEFAULT_PIPELINE_LIST_SUMMARY_MAX_CHARS).text
    : undefined;
  return {
    stepId: step.stepId,
    status: step.status,
    ...(summary !== undefined ? { summary } : {}),
    ...(error !== undefined ? { error } : {}),
    ...(step.startedAt ? { startedAt: step.startedAt } : {}),
    ...(step.completedAt ? { completedAt: step.completedAt } : {}),
    ...(step.outputTruncated ? { outputTruncated: true } : {}),
  };
}
