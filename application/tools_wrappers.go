package application

import (
	"fmt"

	"nusashell/application/learn"
	"nusashell/application/tools"
	"nusashell/contracts"
)

type (
	ToolInfo            = tools.ToolInfo
	ToolExecutor        = tools.ToolExecutor
	CodexSearchRequest  = tools.CodexSearchRequest
	CodexSearchResult   = tools.CodexSearchResult
	CodexSearchResponse = tools.CodexSearchResponse
	CodexSearchBackend  = tools.CodexSearchBackend
	CodexSearchFactory  = tools.CodexSearchFactory
	CodexSearchExecutor = tools.CodexSearchExecutor
	ToolFactory         = tools.ToolFactory
	AgentKind           = tools.AgentKind
	FilteredToolbox     = tools.FilteredToolbox
	PipelineAgentRunner = tools.PipelineAgentRunner
	DocsSource          = tools.DocsSource
	DocMeta             = tools.DocMeta
	DocHit              = tools.DocHit
	DocFull             = tools.DocFull
)

const (
	AgentConversation         = tools.AgentConversation
	AgentAutomation           = tools.AgentAutomation
	AgentCompaction           = tools.AgentCompaction
	AgentDelegate             = tools.AgentDelegate
	AgentLearner              = tools.AgentLearner
	AgentMemoryConsolidator   = tools.AgentMemoryConsolidator
	AgentSkillEvolver         = tools.AgentSkillEvolver
	AgentSkillEvaluator       = tools.AgentSkillEvaluator
	learnerResultToolName     = tools.LearnerResultToolName
	compactionSummaryToolName = tools.CompactionSummaryToolName
)

var (
	IsDispatchRoot            = tools.IsDispatchRoot
	DispatchOp                = tools.DispatchOp
	OpArg                     = tools.OpArg
	DispatcherToolInfos       = tools.DispatcherToolInfos
	FilterDispatcherToolInfos = tools.FilterDispatcherToolInfos
	IsACPTool                 = tools.IsACPTool
	IsLearnerBannedTool       = tools.IsLearnerBannedTool
	FilterACPTools            = tools.FilterACPTools
	NewPipelineAgentRunner    = tools.NewPipelineAgentRunner
	buildToolContract         = tools.BuildContract
)

func toToolDef(t tools.ToolInfo) ToolDef {
	return ToolDef{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema}
}

func toToolDefs(infos []tools.ToolInfo) []ToolDef {
	if infos == nil {
		return nil
	}
	out := make([]ToolDef, len(infos))
	for i, t := range infos {
		out[i] = toToolDef(t)
	}
	return out
}

func isLearnerKind(kind AgentKind) bool {
	return tools.IsLearnerKind(kind)
}

func acknowledgeLearnerResult(args string) (string, error) {
	if !learn.ValidLearnerResult(args) {
		return "", fmt.Errorf("invalid learner result: call learn() with stage_reached and consolidate")
	}
	return "recorded", nil
}

// toolFactoryFor wires the default factory from an App's toolbox and the
// dispatcher families. Bare test Apps without a toolbox get a factory that
// still serves AgentCompaction (which needs no toolbox) and nil otherwise.
func toolFactoryFor(a *App) *ToolFactory {
	var toolbox func() []ToolInfo
	if a != nil && a.Toolbox != nil {
		toolbox = a.Toolbox.ListTools
	}
	return &ToolFactory{Toolbox: toolbox, Dispatchers: FilterDispatcherToolInfos}
}

func (a *App) handleToolContracts(req contracts.ToolContractsRequest) (any, *contracts.RPCError) {
	workspace := a.effectiveWorkspace(req.Workspace)
	defs := toolFactoryFor(a).Get(AgentConversation, workspace)
	result := tools.BuildContracts(defs)
	return result, nil
}

func (a *App) handleDocsList() (any, *contracts.RPCError) {
	return a.toolsService().HandleList()
}

func (a *App) handleDocsSearch(req contracts.DocsSearchRequest) (any, *contracts.RPCError) {
	return a.toolsService().HandleSearch(req)
}

func (a *App) handleDocsRead(req contracts.DocReadRequest) (any, *contracts.RPCError) {
	return a.toolsService().HandleRead(req)
}
