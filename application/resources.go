package application

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"nusashell/contracts"
	"nusashell/domain"
)

// ---- skills ----

func skillDTO(s *domain.Skill) contracts.SkillDTO {
	dto := contracts.SkillDTO{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Category:    s.Category,
		State:       string(s.State),
		Origin:      string(s.Origin),
		Pinned:      s.Pinned,
		UsageCount:  s.UsageCount,
		UpdatedAt:   s.UpdatedAt.Format(timeRFC3339),
	}
	if !s.LastUsedAt.IsZero() {
		dto.LastUsedAt = s.LastUsedAt.Format(timeRFC3339)
	}
	if s.State == "" {
		dto.State = string(domain.SkillStateActive)
	}
	if s.Origin == "" {
		dto.Origin = string(domain.SkillOriginUser)
	}
	return dto
}

func (a *App) handleSkillsList() (any, *contracts.RPCError) {
	list := a.Skills.List()
	out := make([]contracts.SkillDTO, 0, len(list))
	for _, s := range list {
		out = append(out, skillDTO(s))
	}
	return contracts.SkillsListResult{Skills: out}, nil
}

func (a *App) handleSkillsRead(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	s, err := a.Skills.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}

func (a *App) handleSkillsSave(req contracts.SkillSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill name is required"}
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill content is required"}
	}
	var s *domain.Skill
	if req.ID != "" {
		existing, err := a.Skills.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		s = existing
	} else {
		s = &domain.Skill{
			ID:     domain.NewULID("skill"),
			State:  domain.SkillStateActive,
			Origin: domain.SkillOriginUser,
		}
	}
	s.Name = name
	s.Description = strings.TrimSpace(req.Description)
	s.Content = req.Content
	s.UpdatedAt = time.Now().UTC()
	if err := a.Skills.Save(s); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill saved: %s", s.Name)
	return contracts.SkillReadResult{Skill: contracts.SkillFull{SkillDTO: skillDTO(s), Content: s.Content}}, nil
}

func (a *App) handleSkillsDelete(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	if _, err := a.Skills.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.Skills.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.log("info", "skills", "skill deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

// handleSkillsRun opens a new conversation primed with the skill instructions.
func (a *App) handleSkillsRun(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	s, err := a.Skills.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	c := domain.NewConversation(domain.NewID("conv"), "Skill: "+s.Name)
	c.AddMessage(domain.Message{
		ID:        domain.NewID("msg"),
		Role:      domain.RoleSystem,
		Content:   s.Content,
		CreatedAt: time.Now().UTC(),
		Status:    domain.StatusDone,
	})
	if err := a.Conversations.Save(c); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.SkillRunResult{ConversationID: c.ID}, nil
}

// ---- MCP ----

func mcpDTO(s *domain.MCPServer) contracts.MCPServerDTO {
	return contracts.MCPServerDTO{
		ID:      s.ID,
		Name:    s.Name,
		Command: s.Command,
		Args:    s.Args,
		Env:     s.Env,
		Enabled: s.Enabled,
	}
}

func (a *App) handleMCPServersList() (any, *contracts.RPCError) {
	list := a.MCP.List()
	out := make([]contracts.MCPServerDTO, 0, len(list))
	for _, s := range list {
		dto := mcpDTO(s)
		dto.Status = "idle"
		if tools, ok := a.MCPToolbox.ToolsFor(s.ID); ok {
			dto.Status = "connected"
			dto.Tools = tools
		}
		out = append(out, dto)
	}
	return contracts.MCPServersListResult{Servers: out}, nil
}

func (a *App) handleMCPServersSave(req contracts.MCPSaveRequest) (any, *contracts.RPCError) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "server name is required"}
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "command is required"}
	}
	var s *domain.MCPServer
	if req.ID != "" {
		existing, err := a.MCP.Get(req.ID)
		if err != nil {
			return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
		}
		s = existing
	} else {
		s = &domain.MCPServer{ID: domain.NewID("mcp")}
	}
	s.Name = name
	s.Command = command
	s.Args = req.Args
	s.Env = req.Env
	s.Enabled = req.Enabled
	if err := a.MCP.Save(s); err != nil {
		return nil, rpcInternal(err)
	}
	a.MCPToolbox.Drop(s.ID)
	a.log("info", "mcp", "mcp server saved: %s", s.Name)
	return contracts.MCPServersListResult{Servers: []contracts.MCPServerDTO{mcpDTO(s)}}, nil
}

func (a *App) handleMCPServersDelete(req contracts.MCPIDRequest) (any, *contracts.RPCError) {
	if _, err := a.MCP.Get(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	if err := a.MCP.Delete(req.ID); err != nil {
		return nil, rpcInternal(err)
	}
	a.MCPToolbox.Drop(req.ID)
	a.log("info", "mcp", "mcp server deleted: %s", req.ID)
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleMCPServersTest(req contracts.MCPIDRequest) (any, *contracts.RPCError) {
	s, err := a.MCP.Get(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tools, err := a.MCPToolbox.Connect(ctx, s)
	if err != nil {
		a.log("warn", "mcp", "mcp test failed: %s: %v", s.Name, err)
		return nil, &contracts.RPCError{Code: contracts.CodeProvider, Message: err.Error()}
	}
	return contracts.MCPTestResult{Tools: tools}, nil
}

func (a *App) handleMCPToolsList() (any, *contracts.RPCError) {
	var out []contracts.MCPToolDTO
	for _, s := range a.MCP.List() {
		if !s.Enabled {
			continue
		}
		if tools, ok := a.MCPToolbox.ToolsFor(s.ID); ok {
			out = append(out, tools...)
		}
	}
	if out == nil {
		out = []contracts.MCPToolDTO{}
	}
	return contracts.MCPToolsListResult{Tools: out}, nil
}

// ---- memory ----

func memDTO(e *domain.MemoryEntry) contracts.MemoryEntryDTO {
	dto := contracts.MemoryEntryDTO{
		ID:        e.ID,
		Content:   e.Content,
		Tags:      e.Tags,
		Source:    e.Source,
		CreatedAt: e.CreatedAt.Format(timeRFC3339),
	}
	if dto.Source == "" {
		dto.Source = "user"
	}
	return dto
}

func (a *App) handleMemoryList() (any, *contracts.RPCError) {
	list := a.Memory.List()
	out := make([]contracts.MemoryEntryDTO, 0, len(list))
	for _, e := range list {
		out = append(out, memDTO(e))
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemorySave(req contracts.MemorySaveRequest) (any, *contracts.RPCError) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "memory content is required"}
	}
	e := &domain.MemoryEntry{
		ID:        domain.NewULID("mem"),
		Content:   content,
		Tags:      req.Tags,
		Source:    "user",
		CreatedAt: time.Now().UTC(),
	}
	if err := a.Memory.Save(e); err != nil {
		return nil, rpcInternal(err)
	}
	return contracts.MemoryListResult{Entries: []contracts.MemoryEntryDTO{memDTO(e)}}, nil
}

func (a *App) handleMemorySearch(req contracts.MemorySearchRequest) (any, *contracts.RPCError) {
	query := strings.ToLower(strings.TrimSpace(req.Query))
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var out []contracts.MemoryEntryDTO
	for _, e := range a.Memory.List() {
		hay := strings.ToLower(e.Content + " " + strings.Join(e.Tags, " "))
		if query == "" || strings.Contains(hay, query) {
			out = append(out, memDTO(e))
			if len(out) >= limit {
				break
			}
		}
	}
	if out == nil {
		out = []contracts.MemoryEntryDTO{}
	}
	return contracts.MemoryListResult{Entries: out}, nil
}

func (a *App) handleMemoryDelete(req contracts.MemoryIDRequest) (any, *contracts.RPCError) {
	if err := a.Memory.Delete(req.ID); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return map[string]bool{"ok": true}, nil
}

// ---- learning search ----

// handleLearningSearch runs hybrid BM25 + embedding search over skills and
// memory entries, fused via RRF. The kind filter ("skills" or "memory")
// restricts the search to one collection; empty searches both.
func (a *App) handleLearningSearch(req contracts.LearningSearchRequest) (any, *contracts.RPCError) {
	query := strings.TrimSpace(req.Query)
	limit := req.Limit
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	searcher := a.learningSearch()
	ctx := context.Background()
	items := make([]contracts.LearningSearchResultItem, 0, limit*2)

	if kind == "" || kind == "skills" {
		results, err := searcher.SearchSkills(ctx, query, limit)
		if err == nil {
			for _, r := range results {
				s, err := a.Skills.Get(r.ID)
				if err != nil {
					continue
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      s.ID,
					Kind:    "skill",
					Name:    s.Name,
					Content: truncate(s.Content, 200),
					Score:   float32(r.Score),
				})
			}
		}
	}
	if kind == "" || kind == "memory" {
		results, err := searcher.SearchMemory(ctx, query, limit)
		if err == nil {
			for _, r := range results {
				var content string
				for _, e := range a.Memory.List() {
					if e.ID == r.ID {
						content = e.Content
						break
					}
				}
				if content == "" {
					continue
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      r.ID,
					Kind:    "memory",
					Content: truncate(content, 200),
					Score:   float32(r.Score),
				})
			}
		}
	}
	// Sort by score descending.
	sort.Slice(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	if len(items) > limit {
		items = items[:limit]
	}
	if a.Trajectory != nil {
		a.Trajectory.Record("search", map[string]interface{}{
			"query":  query,
			"kind":   kind,
			"limit":  limit,
			"result": len(items),
		})
	}
	return contracts.LearningSearchResult{Items: items}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// handleLearningGraph returns the full learning graph (nodes + edges)
// for the frontend graph view. Nodes are skills + memory entries; edges
// are pre-computed by the EdgeBuilder (similarity + token overlap).
func (a *App) handleLearningGraph() (any, *contracts.RPCError) {
	// Build edges if edge builder is configured (idempotent — strengthens
	// existing edges, doesn't duplicate).
	if a.edgeBuilder != nil {
		// Resolve embedder lazily for embedding-based edges
		if a.edgeBuilder.embed == nil {
			if embedder, modelID := a.resolveEmbedder(); embedder != nil {
				a.edgeBuilder.embed = embedder
				a.edgeBuilder.modelID = modelID
			}
		}
		_ = a.edgeBuilder.Build(context.Background())
	}

	// Collect nodes
	var nodes []contracts.LearningGraphNode
	for _, s := range a.Skills.List() {
		nodes = append(nodes, contracts.LearningGraphNode{
			ID:   s.ID,
			Kind: "skill",
			Name: s.Name,
		})
	}
	for _, m := range a.Memory.List() {
		name := m.Content
		if len(name) > 40 {
			name = name[:40] + "…"
		}
		nodes = append(nodes, contracts.LearningGraphNode{
			ID:   m.ID,
			Kind: "memory",
			Name: name,
		})
	}

	// Collect edges from graph service
	var edges []contracts.LearningGraphEdge
	if gs := a.graph(); gs != nil {
		for _, e := range gs.AllEdges() {
			edges = append(edges, contracts.LearningGraphEdge{
				From:   e.SourceID,
				To:     e.TargetID,
				Type:   string(e.Type),
				Weight: e.Weight,
			})
		}
	}

	if a.Trajectory != nil {
		a.Trajectory.Record("graph_load", map[string]interface{}{
			"nodes": len(nodes),
			"edges": len(edges),
		})
	}
	return contracts.LearningGraphResult{Nodes: nodes, Edges: edges}, nil
}

// ---- docs ----

func (a *App) handleDocsList() (any, *contracts.RPCError) {
	metas := a.Docs.List()
	out := make([]contracts.DocDTO, 0, len(metas))
	for _, m := range metas {
		out = append(out, contracts.DocDTO{ID: m.ID, Title: m.Title, Path: m.Path})
	}
	return contracts.DocsListResult{Docs: out}, nil
}

func (a *App) handleDocsSearch(req contracts.DocsSearchRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 20 {
		limit = 10
	}
	hits := a.Docs.Search(req.Query, limit)
	out := make([]contracts.DocHit, 0, len(hits))
	for _, h := range hits {
		out = append(out, contracts.DocHit{
			DocDTO:  contracts.DocDTO{ID: h.ID, Title: h.Title, Path: h.Path},
			Snippet: h.Snippet,
		})
	}
	return contracts.DocsSearchResult{Results: out}, nil
}

func (a *App) handleDocsRead(req contracts.DocReadRequest) (any, *contracts.RPCError) {
	doc, err := a.Docs.Read(req.ID)
	if err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeNotFound, Message: err.Error()}
	}
	return contracts.DocReadResult{Doc: contracts.DocFull{
		DocDTO:  contracts.DocDTO{ID: doc.ID, Title: doc.Title, Path: doc.Path},
		Content: doc.Content,
	}}, nil
}

// ---- logs / settings ----

func (a *App) handleLogsList(req contracts.LogsListRequest) (any, *contracts.RPCError) {
	limit := req.Limit
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	entries := a.Logs.List(req.Level, limit)
	out := make([]contracts.LogEntryDTO, 0, len(entries))
	for _, e := range entries {
		out = append(out, contracts.LogEntryDTO{
			ID: e.ID, Time: e.Time.Format(timeRFC3339), Level: e.Level, Source: e.Source, Message: e.Message,
		})
	}
	return contracts.LogsListResult{Entries: out}, nil
}

func (a *App) handleLogsClear() (any, *contracts.RPCError) {
	a.Logs.Clear()
	return map[string]bool{"ok": true}, nil
}

func (a *App) handleSettingsGet() (any, *contracts.RPCError) {
	return contracts.SettingsGetResult{Settings: settingsDTO(a.Settings.Get())}, nil
}

func (a *App) handleSettingsSet(req contracts.SettingsSetRequest) (any, *contracts.RPCError) {
	s := a.Settings.Get()
	if req.CompactionEnabled != nil {
		s.CompactionEnabled = *req.CompactionEnabled
	}
	if req.CompactionThreshold != nil {
		if *req.CompactionThreshold < 1000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction threshold must be at least 1000 tokens"}
		}
		s.CompactionThreshold = *req.CompactionThreshold
	}
	if req.PromptCaching != nil {
		s.PromptCaching = *req.PromptCaching
	}
	if req.MaxToolRounds != nil {
		if *req.MaxToolRounds < 1 || *req.MaxToolRounds > 10000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max tool rounds must be between 1 and 10000"}
		}
		s.MaxToolRounds = *req.MaxToolRounds
	}
	if req.MaxInputTokens != nil {
		if *req.MaxInputTokens < 1000 || *req.MaxInputTokens > 2000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max input tokens must be between 1000 and 2000000"}
		}
		s.MaxInputTokens = *req.MaxInputTokens
	}
	if req.MaxOutputTokens != nil {
		if *req.MaxOutputTokens < 256 || *req.MaxOutputTokens > 1000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max output tokens must be between 256 and 1000000"}
		}
		s.MaxOutputTokens = *req.MaxOutputTokens
	}
	// Sampling parameters use json.RawMessage to distinguish three states:
	// absent (don't change), null (clear to nil), value (set). A *float64
	// with omitempty cannot tell null from absent, so once set the parameter
	// could never be cleared.
	if err := applyOptionalFloat(req.Temperature, func(v float64) error {
		if v < 0 || v > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
		return nil
	}, &s.Temperature); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.TopP, func(v float64) error {
		if v < 0 || v > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
		return nil
	}, &s.TopP); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalInt(req.TopK, func(v int) error {
		if v < 1 {
			return fmt.Errorf("top_k must be at least 1")
		}
		return nil
	}, &s.TopK); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.FrequencyPenalty, func(v float64) error {
		if v < -2 || v > 2 {
			return fmt.Errorf("frequency_penalty must be between -2 and 2")
		}
		return nil
	}, &s.FrequencyPenalty); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := applyOptionalFloat(req.PresencePenalty, func(v float64) error {
		if v < -2 || v > 2 {
			return fmt.Errorf("presence_penalty must be between -2 and 2")
		}
		return nil
	}, &s.PresencePenalty); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if req.EmbeddingProviderID != nil {
		s.EmbeddingProviderID = strings.TrimSpace(*req.EmbeddingProviderID)
	}
	if req.EmbeddingModelID != nil {
		s.EmbeddingModelID = strings.TrimSpace(*req.EmbeddingModelID)
	}
	if req.VisionProviderID != nil {
		s.VisionProviderID = strings.TrimSpace(*req.VisionProviderID)
	}
	if req.VisionModelID != nil {
		s.VisionModelID = strings.TrimSpace(*req.VisionModelID)
	}
	if req.LearningReviewThreshold != nil {
		v := *req.LearningReviewThreshold
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "learning_review_threshold must be >= 0 (0 disables turn-based review)"}
		}
		s.LearningReviewThreshold = v
	}
	if err := a.Settings.Set(s); err != nil {
		return nil, rpcInternal(err)
	}
	// Invalidate the learning searcher so the next search rebuilds it with
	// the new embedding settings (if the embedding model selection changed).
	a.InvalidateLearningSearcher()
	return contracts.SettingsGetResult{Settings: settingsDTO(s)}, nil
}

func settingsDTO(s domain.Settings) contracts.SettingsDTO {
	return contracts.SettingsDTO{
		CompactionEnabled:       s.CompactionEnabled,
		CompactionThreshold:     s.CompactionThreshold,
		PromptCaching:           s.PromptCaching,
		MaxToolRounds:           s.MaxToolRounds,
		MaxInputTokens:          s.MaxInputTokens,
		MaxOutputTokens:         s.MaxOutputTokens,
		EmbeddingProviderID:     s.EmbeddingProviderID,
		EmbeddingModelID:        s.EmbeddingModelID,
		VisionProviderID:        s.VisionProviderID,
		VisionModelID:           s.VisionModelID,
		Temperature:             s.Temperature,
		TopP:                    s.TopP,
		TopK:                    s.TopK,
		FrequencyPenalty:        s.FrequencyPenalty,
		PresencePenalty:         s.PresencePenalty,
		LearningReviewThreshold: s.LearningReviewThreshold,
	}
}

// applyOptionalFloat parses a json.RawMessage sampling parameter in three
// states: nil (absent — don't change), "null" (clear to nil), or a float64
// value (validate then set). A *float64 with omitempty cannot distinguish
// null from absent, so json.RawMessage is used on the wire instead.
func applyOptionalFloat(raw json.RawMessage, validate func(float64) error, target **float64) error {
	if len(raw) == 0 {
		return nil
	}
	if string(raw) == "null" {
		*target = nil
		return nil
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid number: %w", err)
	}
	if err := validate(v); err != nil {
		return err
	}
	*target = &v
	return nil
}

// applyOptionalInt is the integer variant of applyOptionalFloat for top_k.
func applyOptionalInt(raw json.RawMessage, validate func(int) error, target **int) error {
	if len(raw) == 0 {
		return nil
	}
	if string(raw) == "null" {
		*target = nil
		return nil
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("invalid integer: %w", err)
	}
	if err := validate(v); err != nil {
		return err
	}
	*target = &v
	return nil
}
