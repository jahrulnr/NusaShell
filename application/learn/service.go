package learn

import (
	"context"
	"strings"
	"sync"

	"nusashell/domain"
)

// Service owns the experience-learning pipeline: recording, jobs, search,
// graph/edges, lifecycle decay, and learning.* / experience.* RPC.
type Service struct {
	deps Deps

	mu        sync.RWMutex
	searcher  *LearningSearcher
	graphSvc  *LearningGraphService
	builder   *EdgeBuilder
	lifecycle *LifecycleManager
}

// New builds a learn Service from Deps.
func New(d Deps) *Service {
	goFn := d.Go
	if goFn == nil {
		goFn = func(_ string, fn func()) { go fn() }
	}
	d.Go = goFn
	s := &Service{deps: d}
	if d.Records != nil {
		s.lifecycle = NewLifecycleManager(d.Records, d.Skills, domain.DefaultLifecycleConfig())
		s.lifecycle.SetLogger(d.Log)
	}
	return s
}

// InitEdgeBuilder wires the background edge builder. Called from the
// composition root (NewApp) so test App literals keep builder nil and
// graph RPC can filter dangling edges without pruning the fake store.
func (s *Service) InitEdgeBuilder() {
	if s == nil || s.builder != nil || s.deps.Records == nil || s.deps.Skills == nil {
		return
	}
	s.builder = NewEdgeBuilder(
		s.deps.Records, s.deps.Skills, s.Graph(),
		nil,
		s.deps.EmbeddingCache,
		DefaultEdgeBuilderConfig(),
		"",
	)
	s.builder.SetUserStore(s.deps.User)
}

func (s *Service) log(level, source, format string, args ...any) {
	if s != nil && s.deps.Log != nil {
		s.deps.Log(level, source, format, args...)
	}
}

func (s *Service) emit(typ string, v any) {
	if s != nil && s.deps.Bus != nil {
		s.deps.Bus.Emit(typ, v)
	}
}

func (s *Service) goSafe(name string, fn func()) {
	if s == nil || s.deps.Go == nil {
		go fn()
		return
	}
	s.deps.Go(name, fn)
}

// LearningSearch returns the lazy-initialized LearningSearcher.
func (s *Service) LearningSearch() *LearningSearcher {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.searcher != nil {
		return s.searcher
	}
	var embed Embedder
	if s.deps.EmbedderFactory != nil && s.deps.Providers != nil && s.deps.Credentials != nil {
		providerID := ""
		if s.deps.Settings != nil {
			providerID = s.deps.Settings.Get().EmbeddingProviderID
		}
		embed = ResolveEmbedder(s.deps.Providers, s.deps.Credentials, s.deps.EmbedderFactory, providerID)
	}
	if s.graphSvc == nil && s.deps.Edges != nil {
		s.graphSvc = NewLearningGraphService(s.deps.Edges)
	}
	s.searcher = NewLearningSearcher(s.deps.Skills, s.deps.Records, embed, s.graphSvc, s.deps.NewKeywordIndex)
	return s.searcher
}

// SearchSkills ranks skills with BM25 + graph + recency, embedding off.
func (s *Service) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	searcher := s.LearningSearch()
	if searcher == nil {
		return nil, nil
	}
	opts := DefaultSearchOptions()
	opts.DisableEmbedding = true
	return searcher.SearchSkillsWithOpts(ctx, query, topK, opts)
}

// Graph returns the lazy-initialized LearningGraphService.
func (s *Service) Graph() *LearningGraphService {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.graphSvc != nil {
		return s.graphSvc
	}
	if s.deps.Edges == nil {
		return nil
	}
	s.graphSvc = NewLearningGraphService(s.deps.Edges)
	return s.graphSvc
}

// ResolveEmbedderPair returns the configured embedder and its model ID.
func (s *Service) ResolveEmbedderPair() (Embedder, string) {
	if s == nil || s.deps.EmbedderFactory == nil || s.deps.Providers == nil || s.deps.Credentials == nil {
		return nil, ""
	}
	providerID, modelID := "", ""
	if s.deps.Settings != nil {
		st := s.deps.Settings.Get()
		providerID = st.EmbeddingProviderID
		modelID = st.EmbeddingModelID
	}
	embed := ResolveEmbedder(s.deps.Providers, s.deps.Credentials, s.deps.EmbedderFactory, providerID)
	if embed == nil {
		return nil, ""
	}
	return embed, modelID
}

// InvalidateSearcher forces the next LearningSearch call to rebuild.
func (s *Service) InvalidateSearcher() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.searcher = nil
}

// PruneEdges removes every graph edge touching nodeID.
func (s *Service) PruneEdges(nodeID string) {
	gs := s.Graph()
	if gs == nil || strings.TrimSpace(nodeID) == "" {
		return
	}
	for _, e := range gs.AllEdges() {
		if e == nil || e.InvalidAt != nil {
			continue
		}
		if e.SourceID == nodeID || e.TargetID == nodeID {
			_ = gs.DeleteEdge(e.ID)
		}
	}
}

// RunLifecycle starts the decay/prune loop. Blocks until ctx is cancelled.
func (s *Service) RunLifecycle(ctx context.Context) {
	if s == nil || s.lifecycle == nil {
		return
	}
	s.lifecycle.Run(ctx)
}

// PruneOnce runs a single prune cycle.
func (s *Service) PruneOnce() {
	if s != nil && s.lifecycle != nil {
		s.lifecycle.PruneOnce()
	}
}

func (s *Service) emitMemoryUpdated() {
	if s != nil && s.deps.OnMemoryUpdated != nil {
		s.deps.OnMemoryUpdated()
	}
}
