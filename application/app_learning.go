package application

import (
	"context"
)

// learningSearch returns the lazy-initialized LearningSearcher. The
// searcher is built on first call so it sees the latest embedding settings
// (which may be configured after App construction via the settings UI).
func (a *App) learningSearch() *LearningSearcher {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	if a.learningSearcher != nil {
		return a.learningSearcher
	}
	var embed Embedder
	if a.EmbedderFactory != nil {
		st := a.Settings.Get()
		embed = ResolveEmbedder(a.Providers, a.Credentials, a.EmbedderFactory, st.EmbeddingProviderID)
	}
	// Inline graph init to avoid re-locking learningMu (graph() also locks).
	if a.graphService == nil {
		if a.LearningEdges != nil {
			a.graphService = NewLearningGraphService(a.LearningEdges)
		}
	}
	a.learningSearcher = NewLearningSearcher(a.Skills, a.Memory, embed, a.graphService)
	return a.learningSearcher
}

// SearchSkills implements SkillSearcher for the skill tool (op=search). It ranks
// with BM25 + graph + recency but forces embedding off — per-call embedding
// cost in the agent loop is not justified; the Learning UI keeps the full
// hybrid path.
func (a *App) SearchSkills(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	s := a.learningSearch()
	if s == nil {
		return nil, nil
	}
	opts := defaultSearchOptions()
	opts.DisableEmbedding = true
	return s.SearchSkillsWithOpts(ctx, query, topK, opts)
}

// graph returns the lazy-initialized LearningGraphService.
func (a *App) graph() *LearningGraphService {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	if a.graphService != nil {
		return a.graphService
	}
	if a.LearningEdges == nil {
		return nil
	}
	a.graphService = NewLearningGraphService(a.LearningEdges)
	return a.graphService
}

// resolveEmbedder returns the configured embedder and its model ID.
// Returns nil, "" if no embedder is available.
func (a *App) resolveEmbedder() (Embedder, string) {
	if a.EmbedderFactory == nil {
		return nil, ""
	}
	st := a.Settings.Get()
	embed := ResolveEmbedder(a.Providers, a.Credentials, a.EmbedderFactory, st.EmbeddingProviderID)
	if embed == nil {
		return nil, ""
	}
	return embed, st.EmbeddingModelID
}

// InvalidateLearningSearcher forces the next learningSearch() call to
// rebuild the searcher with fresh embedding settings. Called when the
// embedding model selection changes in settings.
func (a *App) InvalidateLearningSearcher() {
	a.learningMu.Lock()
	defer a.learningMu.Unlock()
	a.learningSearcher = nil
}
