package automation

import (
	"context"
	"encoding/json"

	"nusashell/contracts"
	"nusashell/pkg/rpcdispatch"
)

// Dispatch routes automation.* RPC methods. steer is injected so this
// package never holds *App; App implements HeadlessTurnRunner.
func (a *Automation) Dispatch(ctx context.Context, method string, payload json.RawMessage, steer HeadlessTurnRunner) (any, *contracts.RPCError) {
	if a == nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: "automation is not configured"}
	}
	return rpcdispatch.Table(map[string]rpcdispatch.Handler{
		contracts.MethodAutomationValidate: rpcdispatch.DecodeReq(a.handleValidate),
		contracts.MethodAutomationRunsStart: rpcdispatch.DecodeReq(func(req contracts.AutomationRunStartRequest) (any, *contracts.RPCError) {
			return a.handleRunsStart(ctx, req)
		}),
		contracts.MethodAutomationRunsList: rpcdispatch.NoPayload(func() (any, *contracts.RPCError) {
			return a.handleRunsList(ctx)
		}),
		contracts.MethodAutomationRunsGet: rpcdispatch.DecodeReq(func(req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
			return a.handleRunsGet(ctx, req)
		}),
		contracts.MethodAutomationRunsCancel: rpcdispatch.DecodeReq(func(req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
			return a.handleRunsCancel(ctx, req)
		}),
		contracts.MethodAutomationRunsSteer: rpcdispatch.DecodeReq(func(req contracts.AutomationRunSteerRequest) (any, *contracts.RPCError) {
			return a.handleRunsSteer(ctx, steer, req)
		}),
		contracts.MethodAutomationRunsRetry: rpcdispatch.DecodeReq(func(req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
			return a.handleRunsRetry(ctx, req)
		}),
		contracts.MethodAutomationJobsLogs: rpcdispatch.DecodeReq(func(req contracts.AutomationLogsRequest) (any, *contracts.RPCError) {
			return a.handleJobsLogs(ctx, req)
		}),
		contracts.MethodAutomationJobsGet: rpcdispatch.DecodeReq(func(req contracts.AutomationLogsRequest) (any, *contracts.RPCError) {
			return a.handleJobsGet(ctx, req)
		}),
		contracts.MethodAutomationJobsCancel: rpcdispatch.DecodeReq(func(req contracts.AutomationRunIDRequest) (any, *contracts.RPCError) {
			return a.handleJobsCancel(ctx, req)
		}),
		contracts.MethodAutomationArtifactsList: rpcdispatch.NoPayload(a.handleArtifactsList),
		contracts.MethodAutomationCacheList:     rpcdispatch.NoPayload(a.handleCacheList),
		contracts.MethodAutomationCacheClear:    rpcdispatch.NoPayload(a.handleCacheClear),
		contracts.MethodAutomationRunnersList:   rpcdispatch.NoPayload(a.handleRunnersList),
		contracts.MethodAutomationList: rpcdispatch.NoPayload(func() (any, *contracts.RPCError) {
			return a.handleList(ctx)
		}),
		contracts.MethodAutomationGet: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
			return a.handleGet(ctx, req)
		}),
		contracts.MethodAutomationSave: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowSaveRequest) (any, *contracts.RPCError) {
			return a.handleSave(ctx, req)
		}),
		contracts.MethodAutomationDelete: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
			return a.handleDelete(ctx, req)
		}),
		contracts.MethodAutomationEnable: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
			return a.handleSetEnabled(ctx, req, true)
		}),
		contracts.MethodAutomationDisable: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
			return a.handleSetEnabled(ctx, req, false)
		}),
		contracts.MethodAutomationRun: rpcdispatch.DecodeReq(func(req contracts.AutomationWorkflowIDRequest) (any, *contracts.RPCError) {
			return a.handleRun(ctx, req)
		}),
		contracts.MethodAutomationEvents: rpcdispatch.NoPayload(func() (any, *contracts.RPCError) {
			return a.handleEvents(ctx)
		}),
		contracts.MethodAutomationIngest: rpcdispatch.DecodeReq(func(req contracts.AutomationIngestRequest) (any, *contracts.RPCError) {
			return a.handleIngest(ctx, req)
		}),
		contracts.MethodAutomationSchedules: rpcdispatch.NoPayload(func() (any, *contracts.RPCError) {
			return a.handleSchedules(ctx)
		}),
		contracts.MethodAutomationCapabilities: rpcdispatch.NoPayload(func() (any, *contracts.RPCError) {
			return a.handleCapabilities(ctx)
		}),
		contracts.MethodAutomationDependents: rpcdispatch.DecodeReq(func(req contracts.PluginIDRequest) (any, *contracts.RPCError) {
			return a.handleDependents(ctx, req)
		}),
		contracts.MethodAutomationProviderDisable: rpcdispatch.DecodeReq(func(req contracts.PluginSetFlagRequest) (any, *contracts.RPCError) {
			return a.handleProviderDisable(ctx, req)
		}),
	}, "automation")(method, payload)
}
