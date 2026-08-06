import type { QueryHandler } from "../../../messaging/query-handler.js";
import type { PipelineRun, PipelineStepRun } from "../../pipeline-model.js";
import {
  boundPipelineText,
  DEFAULT_PIPELINE_LIST_SUMMARY_MAX_CHARS,
} from "../../pipeline-output.js";
import type { PipelineStorePort } from "../../ports/pipeline-store.port.js";
import type { GetPipelineRunQuery, GetPipelineRunResult } from "./get-pipeline-run.query.js";

export class GetPipelineRunHandler implements QueryHandler<GetPipelineRunQuery, GetPipelineRunResult> {
  constructor(private readonly store: PipelineStorePort) {}

  async handle(query: GetPipelineRunQuery): Promise<GetPipelineRunResult> {
    const run = await this.store.getRun(query.runId);
    if (!run) return { run: null };
    if (query.includeBody === true) return { run };
    return { run: compactRun(run) };
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
