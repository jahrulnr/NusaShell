// Package automation owns YAML pipelines, triggers, the DAG
// executor, scheduler, webhook notifications, and the in-process
// AutomationStore (workflows/runs/schedules). Agent steps run through
// HeadlessTurnRunner / AgentStepRunner ports. Pipeline agent-step tools
// (FilteredToolbox, PipelineAgentRunner) stay in the root application
// package until tools/ because they sit on ToolExecutor / ToolInfo.
package automation
