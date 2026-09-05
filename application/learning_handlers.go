package application

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"
)

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

	// Empty query: return an unfiltered listing (all skills and/or memories)
	// so the Learning view shows content immediately instead of an empty
	// "search to begin" state. Score is 0; items are sorted by name.
	if query == "" {
		items := make([]contracts.LearningSearchResultItem, 0, limit*2)
		if kind == "" || kind == "skills" {
			for _, sk := range a.Skills.List() {
				items = append(items, contracts.LearningSearchResultItem{
					ID:      sk.ID,
					Kind:    "skill",
					Name:    sk.Name,
					Content: sk.Content,
				})
			}
		}
		if kind == "" || kind == "memory" {
			// User memory entries.
			if a.User != nil {
				mem := a.User.Load()
				for i := range mem.Entries {
					content := mem.Entries[i].Content
					name := content
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      mem.Entries[i].ID,
						Kind:    "memory",
						Tier:    domain.MemoryTierUser,
						Name:    name,
						Content: content,
					})
				}
			}
			// Fragments.
			if a.MemoryRecords != nil {
				for _, f := range a.MemoryRecords.List() {
					if f == nil || !f.Retrievable() {
						continue
					}
					name := f.Body
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      f.ID,
						Kind:    "memory",
						Tier:    contracts.MemoryTierRecord,
						Name:    name,
						Content: f.Body,
					})
				}
			}
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
		if len(items) > limit {
			items = items[:limit]
		}
		return contracts.LearningSearchResult{Items: items}, nil
	}

	searcher := a.learningSearch()
	ctx := context.Background()
	items := make([]contracts.LearningSearchResultItem, 0, limit*2)

	if kind == "" || kind == "skills" {
		results, err := searcher.SearchSkills(ctx, query, limit)
		if err == nil {
			for _, r := range results {
				s, err := a.Skills.Get(r.ID, "")
				if err != nil {
					continue
				}
				items = append(items, contracts.LearningSearchResultItem{
					ID:      s.ID,
					Kind:    "skill",
					Name:    s.Name,
					Content: s.Content,
					Score:   float32(r.Score),
				})
			}
		}
	}
	if kind == "" || kind == "memory" {
		if a.MemoryRecords != nil {
			results, err := searcher.SearchMemory(ctx, query, limit)
			if err == nil {
				for _, r := range results {
					m, err := a.MemoryRecords.Get(r.ID)
					if err != nil || m == nil {
						continue
					}
					name := m.Body
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      m.ID,
						Kind:    "memory",
						Tier:    contracts.MemoryTierRecord,
						Name:    name,
						Content: m.Body,
						Score:   float32(r.Score),
					})
				}
			}
		}
		// Also search user memory via substring.
		if a.User != nil {
			mem := a.User.Load()
			q := strings.ToLower(query)
			for i := range mem.Entries {
				if strings.Contains(strings.ToLower(mem.Entries[i].Content), q) {
					name := mem.Entries[i].Content
					if len(name) > 40 {
						name = name[:40] + "…"
					}
					items = append(items, contracts.LearningSearchResultItem{
						ID:      mem.Entries[i].ID,
						Kind:    "memory",
						Tier:    domain.MemoryTierUser,
						Name:    name,
						Content: mem.Entries[i].Content,
					})
				}
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

// handleLearningGraph returns the full learning graph (nodes + edges)
// for the frontend graph view. Nodes are skills + memory entries; edges
// are pre-computed by the EdgeBuilder (content/embedding similarity plus
// fragment metadata); used_with edges come from successful tool usage.
func (a *App) handleLearningGraph() (any, *contracts.RPCError) {
	// Build edges if edge builder is configured (idempotent — strengthens
	// existing edges, doesn't duplicate).
	if a.edgeBuilder != nil {
		// Resolve embedder lazily for embedding-based edges
		if embedder, modelID := a.resolveEmbedder(); embedder != nil {
			a.edgeBuilder.SetEmbedder(embedder, modelID)
		}
		if err := a.edgeBuilder.Build(context.Background()); err != nil {
			a.log("warn", "learning", "graph build: %v", err)
		}
	}

	// Collect nodes
	nodes := make([]contracts.LearningGraphNode, 0)
	for _, s := range a.Skills.List() {
		nodes = append(nodes, contracts.LearningGraphNode{
			ID:   s.ID,
			Kind: "skill",
			Name: s.Name,
		})
	}
	// User memory nodes. User memory is a single prose document (one entry
	// per whole document), not per-fact entries — the node label is the
	// first line so it reads as the document's subject, and the tier marks
	// it as user memory in the UI (distinct shape/color from fragments).
	if a.User != nil {
		mem := a.User.Load()
		for i := range mem.Entries {
			nodes = append(nodes, contracts.LearningGraphNode{
				ID:   mem.Entries[i].ID,
				Kind: "memory",
				Tier: domain.MemoryTierUser,
				Name: userNodeLabel(mem.Entries[i].Content),
			})
		}
	}
	// Fragment nodes (one node per fact).
	if a.MemoryRecords != nil {
		for _, f := range a.MemoryRecords.List() {
			if f == nil || !f.Retrievable() {
				continue
			}
			nodes = append(nodes, contracts.LearningGraphNode{
				ID:   f.ID,
				Kind: "memory",
				Tier: contracts.MemoryTierRecord,
				Name: memoryNodeLabel(f.Body),
			})
		}
	}

	// Collect edges from graph service. Only edges whose BOTH endpoints are
	// present in the node set are emitted: memory/skill entries that were
	// deleted after an edge was persisted would otherwise reference nodes
	// that do not exist, and vis-network silently drops them — making whole
	// clusters appear disconnected.
	nodeIDs := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		nodeIDs[n.ID] = struct{}{}
	}
	edges := make([]contracts.LearningGraphEdge, 0)
	if gs := a.graph(); gs != nil {
		for _, e := range gs.AllEdges() {
			if e == nil || e.InvalidAt != nil {
				continue
			}
			if _, ok := nodeIDs[e.SourceID]; !ok {
				continue
			}
			if _, ok := nodeIDs[e.TargetID]; !ok {
				continue
			}
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

// memoryNodeLabel shortens a fragment's content to a single-line node
// label (max 40 chars), collapsing whitespace so multi-line content does
// not break the graph label.
func memoryNodeLabel(content string) string {
	oneLine := strings.Join(strings.Fields(content), " ")
	if len(oneLine) > 40 {
		return oneLine[:40] + "…"
	}
	return oneLine
}

// userNodeLabel labels the single user-memory document node with
// its first line (the document's subject), capped at 60 chars. The full
// document stays in the node tooltip via the frontend's title fallback.
func userNodeLabel(content string) string {
	first := content
	if idx := strings.IndexByte(first, '\n'); idx >= 0 {
		first = first[:idx]
	}
	first = strings.TrimSpace(first)
	if len(first) > 60 {
		return first[:60] + "…"
	}
	return first
}

// handleLearningLog returns the autolearn activity feed: learning-layer
// events from the trajectory log (review runs, extraction, edge building,
// consolidation, decay, prune), newest first. Review events are enriched
// with the source conversation title and their mutations (kind, tool,
// snippet). Events that are pure UI query noise (search, graph_load) are
// excluded.
func (a *App) handleLearningLog(req contracts.LearningLogRequest) (any, *contracts.RPCError) {
	events := ReadTrajectory(a.DataDir, req.Limit)

	// Conversation title lookup for review events. Build once from the
	// store (conversation counts are small) rather than per event.
	titles := map[string]string{}
	if a.Conversations != nil {
		for _, c := range a.Conversations.List() {
			if c.Title != "" {
				titles[c.ID] = c.Title
			}
		}
	}

	out := make([]contracts.LearningLogEntryDTO, 0, len(events))
	for _, e := range events {
		entry := contracts.LearningLogEntryDTO{
			TS:   clock.NewTime(e.Timestamp).RFC3339(),
			Type: e.Type,
		}
		if convID, ok := e.Detail["conversation"].(string); ok && convID != "" {
			entry.ConversationID = convID
			entry.ConversationTitle = titles[convID]
		}
		if reviewID, ok := e.Detail["review_id"].(string); ok {
			entry.ReviewID = reviewID
		}
		if llmConvID, ok := e.Detail["llm_conversation_id"].(string); ok {
			entry.LLMConversationID = llmConvID
		}
		if status, ok := e.Detail["status"].(string); ok {
			entry.Status = status
		}
		if errMsg, ok := e.Detail["error"].(string); ok {
			if e.Type == "review" && entry.Status == "error" {
				entry.Error = "Background review failed during automatic processing."
			} else {
				entry.Error = errMsg
			}
		}
		if raw, ok := e.Detail["mutations"]; ok {
			if list, ok := raw.([]interface{}); ok {
				for _, m := range list {
					if mm, ok := m.(map[string]interface{}); ok {
						// Structured mutation: kind + tool + snippet.
						mut := contracts.LearningLogMutationDTO{}
						if kind, ok := mm["kind"].(string); ok {
							mut.Kind = kind
						}
						if tool, ok := mm["tool"].(string); ok {
							mut.Tool = tool
						}
						if snippet, ok := mm["snippet"].(string); ok {
							mut.Snippet = snippet
						}
						entry.Mutations = append(entry.Mutations, mut)
						continue
					}
					if s, ok := m.(string); ok {
						// Legacy entries recorded mutations as a list of
						// kind strings (e.g. ["memory"]).
						entry.Mutations = append(entry.Mutations, contracts.LearningLogMutationDTO{Kind: s})
					}
				}
			}
		}
		// Pass through the remaining detail fields as raw JSON so the UI
		// can show per-type extras (e.g. decay/prune counts). The
		// conversation and mutations fields are structured columns and
		// must not be duplicated here.
		if len(e.Detail) > 0 {
			detail := make(map[string]json.RawMessage, len(e.Detail))
			for k, v := range e.Detail {
				if k == "conversation" || k == "mutations" || k == "review_id" || k == "llm_conversation_id" || k == "status" || k == "error" {
					continue
				}
				b, err := json.Marshal(v)
				if err == nil {
					detail[k] = b
				}
			}
			if len(detail) > 0 {
				entry.Detail = detail
			}
		}
		out = append(out, entry)
	}
	return contracts.LearningLogResult{Entries: out}, nil
}
