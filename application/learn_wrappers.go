package application

import (
	"context"
	"encoding/json"

	"nusashell/application/learn"
	"nusashell/contracts"
	"nusashell/domain"
	"nusashell/infrastructure/jsonstore"
)

type (
	SearchResult         = learn.SearchResult
	SearchOptions        = learn.SearchOptions
	LearningSearcher     = learn.LearningSearcher
	EdgeBuilder          = learn.EdgeBuilder
	EdgeBuilderConfig    = learn.EdgeBuilderConfig
	LearningGraphService = learn.LearningGraphService
	LifecycleManager     = learn.LifecycleManager
	LifecycleConfig      = learn.LifecycleConfig
	TrajectoryRecorder   = learn.TrajectoryRecorder
	TrajectoryEvent      = learn.TrajectoryEvent
	KeywordDoc           = learn.KeywordDoc
	KeywordIndex         = learn.KeywordIndex
	llmProposedOp        = learn.LLMProposedOp
	llmSkillProposal     = learn.LLMSkillProposal
	learnerResult        = learn.LearnerResult
	rrfResult            = learn.RRFResult
)

var (
	NewLearningSearcher                 = newRootLearningSearcher
	NewEdgeBuilder                      = learn.NewEdgeBuilder
	DefaultEdgeBuilderConfig            = learn.DefaultEdgeBuilderConfig
	NewLearningGraphService             = learn.NewLearningGraphService
	NewLifecycleManager                 = learn.NewLifecycleManager
	NewTrajectoryRecorder               = learn.NewTrajectoryRecorder
	ReadTrajectory                      = learn.ReadTrajectory
	ResolveEmbedder                     = learn.ResolveEmbedder
	parseLearnerResult                  = learn.ParseLearnerResult
	extractJSONFromText                 = learn.ExtractJSONFromText
	parseLLMOperations                  = learn.ParseLLMOperations
	parseLLMOperationsResult            = learn.ParseLLMOperationsResult
	parseLLMSkillProposal               = learn.ParseLLMSkillProposal
	skillMeetsMinimumBar                = learn.SkillMeetsMinimumBar
	teachingOps                         = learn.TeachingOps
	learnedSkillName                    = learn.LearnedSkillName
	findCanonicalLearnedSkill           = learn.FindCanonicalLearnedSkill
	fuseRRF                             = learn.FuseRRF
	collectSeeds                        = learn.CollectSeeds
	learnerTurnOutput                   = learn.LearnerTurnOutput
	opsFromLearnerConsolidate           = learn.OpsFromLearnerConsolidate
	payloadString                       = learn.PayloadString
	detailString                        = learn.DetailString
	newLearnedSkill                     = learn.NewLearnedSkill
	MaxMemoryEntries                    = learn.MaxMemoryEntries
	defaultSearchOptions                = learn.DefaultSearchOptions
	learningMessageRangeForConversation = learn.LearningMessageRangeForConversation
)

func newRootLearningSearcher(skills SkillStore, records MemoryRecordStore, embed Embedder, graph *LearningGraphService) *LearningSearcher {
	return learn.NewLearningSearcher(skills, records, embed, graph, newLearnKeywordIndex)
}

func newLearnKeywordIndex(docs []learn.KeywordDoc) learn.KeywordIndex {
	bm25docs := make([]jsonstore.BM25Doc, len(docs))
	for i, d := range docs {
		bm25docs[i] = jsonstore.BM25Doc{ID: d.ID, Text: d.Text}
	}
	return bm25KeywordIndex{idx: jsonstore.NewBM25(bm25docs)}
}

type bm25KeywordIndex struct{ idx *jsonstore.BM25 }

func (b bm25KeywordIndex) Search(query string, topK int) []learn.SearchResult {
	hits := b.idx.Search(query, topK)
	out := make([]learn.SearchResult, len(hits))
	for i, h := range hits {
		out[i] = learn.SearchResult{ID: h.ID, Score: h.Score}
	}
	return out
}

type learnerHeadless struct{ app *App }

func (h learnerHeadless) RunHeadlessTurn(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any) (map[string]any, string, error) {
	return h.app.runHeadlessTurnKind(ctx, prompt, model, trust, schema, AgentLearner)
}

func (a *App) learningSearch() *LearningSearcher {
	return a.learnService().LearningSearch()
}

func (a *App) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	return a.learnService().SearchSkills(ctx, query, topK)
}

func (a *App) InvalidateLearningSearcher() {
	if a == nil {
		return
	}
	a.learnMu.Lock()
	svc := a.learnSvc
	a.learnMu.Unlock()
	if svc != nil {
		svc.InvalidateSearcher()
	}
}

func (a *App) pruneLearningEdges(nodeID string) {
	a.learnService().PruneEdges(nodeID)
}

func (a *App) recordExperience(conv *domain.Conversation, headless bool) {
	a.learnService().RecordExperience(conv, headless)
}

func (a *App) RecoverStaleLearningJobs() {
	a.learnService().RecoverStaleLearningJobs()
}

func learningNodeIDsFromTool(app *App, toolCall domain.ToolCall, output string) []string {
	if app == nil {
		return nil
	}
	return learn.LearningNodeIDsFromTool(app.learnService(), toolCall, output)
}

func (a *App) emitSkillLifecycle(op, id, status, conversationID string) {
	a.learnService().EmitSkillLifecycle(op, id, status, conversationID)
}

func (a *App) learnerNudgeInterval() int {
	if a == nil || a.Settings == nil {
		return domain.DefaultLearnerNudgeInterval
	}
	return domain.EffectiveLearnerNudgeInterval(a.Settings.Get().LearnerNudgeInterval)
}

func (a *App) dispatchLearning(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return a.learnService().Dispatch(method, payload)
}

func (a *App) dispatchExperience(method string, payload json.RawMessage) (any, *contracts.RPCError) {
	return a.learnService().Dispatch(method, payload)
}
