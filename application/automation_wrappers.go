package application

import (
	"context"
	"encoding/json"

	"nusashell/application/automation"
	"nusashell/contracts"
)

type (
	Automation          = automation.Automation
	AutomationStore     = automation.AutomationStore
	AutomationScheduler = automation.AutomationScheduler
	ExecutionScheduler  = automation.ExecutionScheduler
	YAMLParser          = automation.YAMLParser
	Clock               = automation.Clock
	SystemClock         = automation.SystemClock
	FrozenClock         = automation.FrozenClock
	WorkflowStore       = automation.WorkflowStore
	PipelineDiscoverer  = automation.PipelineDiscoverer
	RunFilter           = automation.RunFilter
	PipelineRunStore    = automation.PipelineRunStore
	ScheduleStore       = automation.ScheduleStore
	EventStore          = automation.EventStore
	WaitStore           = automation.WaitStore
	RunLockStore        = automation.RunLockStore
	ExecutionLogStore   = automation.ExecutionLogStore
	ArtifactPutRequest  = automation.ArtifactPutRequest
	ArtifactStore       = automation.ArtifactStore
	CacheStore          = automation.CacheStore
	RunnerRegistry      = automation.RunnerRegistry
	PrepareRequest      = automation.PrepareRequest
	ExecutionWorkspace  = automation.ExecutionWorkspace
	RunStepRequest      = automation.RunStepRequest
	StepResult          = automation.StepResult
	CleanupRequest      = automation.CleanupRequest
	JobExecutor         = automation.JobExecutor
	CapabilityResolver  = automation.CapabilityResolver
	MCPToolCaller       = automation.MCPToolCaller
	AgentStepRunner     = automation.AgentStepRunner
	HeadlessTurnRunner  = automation.HeadlessTurnRunner
	StepEventSink       = automation.StepEventSink
	StepLifecycleEvent  = automation.StepLifecycleEvent
	NotifyProgressSink  = automation.NotifyProgressSink
	DebounceStore       = automation.DebounceStore
	ProviderStateStore  = automation.ProviderStateStore
	RunNotifier         = automation.RunNotifier
	WorkflowMem         = automation.WorkflowMem
	RunMem              = automation.RunMem
	ScheduleMem         = automation.ScheduleMem
	EventMem            = automation.EventMem
	WaitMem             = automation.WaitMem
	LogMem              = automation.LogMem
	LockMem             = automation.LockMem
	DebounceMem         = automation.DebounceMem
	ProviderStateMem    = automation.ProviderStateMem
)

var (
	NewAutomationStore    = automation.NewAutomationStore
	NewExecutionScheduler = automation.NewExecutionScheduler
	NewWorkflowRun        = automation.NewWorkflowRun
	NewNotifyProgressSink = automation.NewNotifyProgressSink
)

func (a *App) handleAutomation(ctx context.Context, method string, payload json.RawMessage) (any, *contracts.RPCError) {
	if a.Automation == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "automation is not configured"}
	}
	return a.Automation.Dispatch(ctx, method, payload, a)
}
