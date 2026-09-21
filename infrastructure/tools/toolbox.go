// Package tools implements the agent's built-in toolbox (skill_*,
// memory_*, docs_*, mcp_* meta-tools). MCP plugin tools are executed via
// mcp_call with a ref (<plugin-id>:<tool>, e.g. nusashell.files:read);
// mcp__<server>__<tool> names are not callable.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"nusashell/application"
	"nusashell/contracts"
	"nusashell/domain"
	clock "nusashell/pkg/time"

	"github.com/jahrulnr/searchwire"
)

func ptrBool(v bool) *bool { return &v }

type searchwireSearchFunc func(context.Context, *searchwire.Searcher, string, searchwire.SearchOptions) (*searchwire.Response, error)

type Toolbox struct {
	Skills application.SkillStore
	// RuntimeSkills is the read-only discovery view used by agent skill
	// operations. It adds active-workspace and host-global packages without
	// changing the managed store used by learning and UI persistence.
	RuntimeSkills   application.RuntimeSkillCatalog
	SkillSearcher   application.SkillSearcher // optional; nil = substring fallback
	MemoryRecords   application.MemoryRecordStore
	ProjectMemory   application.ProjectMemoryStore
	Docs            application.DocsSource
	Plugins         application.PluginStore
	PluginInstaller application.PluginInstaller
	Todos           application.ConversationTodoPort
	Conversations   application.ConversationMessenger
	Searcher        *searchwire.Searcher // startup searcher for web_fetch + web_search fallback when settings are unavailable
	Providers       application.ProviderStore
	Settings        application.SettingsStore
	Credentials     application.CredentialStore
	CodexSearch     application.CodexSearchExecutor
	AskQuestions    *application.AskQuestionService
	Automation      *application.Automation
	// SpeechOfflineAvailable flips the generate_speech tool on when the local
	// piper engine is wired (set by the composition root at startup).
	SpeechOfflineAvailable bool
	// Contracts reads plugin usage contracts declared via manifest
	// contract.entry. nil disables contract_read and the gate.
	Contracts ContractSource
	MCP       interface {
		Connect(ctx context.Context, p *domain.Plugin) ([]contracts.MCPToolDTO, error)
		ToolsFor(serverID string) ([]contracts.MCPToolDTO, bool)
		CallTool(ctx context.Context, serverID, toolName string, args map[string]any) (string, error)
	}
	Acp interface {
		Subagent(ctx context.Context, argsJSON []byte) (string, error)
		EnabledAcpAgents() []*domain.AcpAgent
	}
	// Delegate spawns internal NusaShell background agents (the
	// `delegate` tool). Optional: nil means the tool is not advertised.
	Delegate interface {
		SpawnDelegate(ctx context.Context, argsJSON []byte) (string, error)
	}
	Steerer interface {
		SteerHeadlessTurn(conversationID, text string) error
	}
	// contractsGate tracks per-conversation contract reads for the gate.
	// Initialized lazily via gate(); safe on the zero value.
	contractsGateOnce sync.Once
	contractsGate     *contractGate
	// execReg tracks detached background exec processes (exec
	// background=true and the status/wait/kill/list ops). Initialized
	// lazily via execRegistry(); terminated by Close at shutdown.
	execRegOnce sync.Once
	execReg     *execRegistry
	// webSearchRR is the round-robin cursor for the web_search provider
	// strategy (Settings → Web Search). Atomic so concurrent tool calls
	// rotate without coordination.
	webSearchRR atomic.Uint64
	// searchwireSearch is an unexported seam for deterministic search tests.
	// Production calls use Searcher.SearchWithOptions directly.
	searchwireSearch searchwireSearchFunc
}

// depMissing is the standard guard error for an optional tool dependency
// that was not wired at startup; msg keeps each site's established wording.
func depMissing(msg string) error {
	return errors.New(msg)
}

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, item := range s {
		if item == v {
			return true
		}
	}
	return false
}

func (t *Toolbox) ListTools() []application.ToolInfo {
	registry := t.toolRegistry()
	tools := make([]application.ToolInfo, 0, len(registry))
	for _, e := range registry {
		if e.enabled != nil && !e.enabled(t) {
			continue
		}
		info := e.info
		if e.describe != nil {
			info.Description = e.describe(t)
		}
		tools = append(tools, info)
	}
	// MCP plugin tools are NOT advertised to the agent. The tool list must
	// stay stable for the lifetime of a conversation so the provider prompt
	// cache (OpenAI / Claude) is not invalidated. The agent discovers MCP
	// tools via mcp_list, tool_list, and tool_schema, and
	// executes them only through mcp_call with a ref (<plugin-id>:<tool>) —
	// mcp__<server>__<tool> names are not callable. MCP tools are available
	// to pipeline workflow steps (capability resolution) and the Plugins UI.
	// Native file CRUD + exec built-ins.
	tools = append(tools, fileToolInfos()...)
	tools = append(tools, execToolInfos()...)
	// Dispatcher roots are part of the Toolbox roster as well as the
	// execution surface. ToolFactory applies workspace/agent policy and
	// deduplicates roots when assembling a turn.
	tools = append(tools, application.DispatcherToolInfos()...)
	return tools
}

// executeFamily routes an advertised dispatcher-root call to its op handler.
// This is the only door to the family
// handlers: the root+"_"+op strings below are private routing keys and are
// not reachable as tool names, so retired per-op calls fail loud upstream.
func (t *Toolbox) executeFamily(ctx context.Context, name string, argsJSON []byte) (string, error) {
	op, err := application.DispatchOp(name, argsJSON)
	if err != nil {
		return "", err
	}
	if name == "automation" || name == "automation_schedule" {
		var privateName string
		if name == "automation" {
			privateName = "automation_" + op
			if op == "status" {
				privateName = "automation_run_status"
			}
		} else {
			privateName = "automation_schedule_" + op
		}
		out, handled, err := t.executeAutomation(ctx, privateName, argsJSON)
		if !handled {
			return "", fmt.Errorf("unknown %s op %q", name, op)
		}
		return out, err
	}
	name = name + "_" + op // private routing key; never escapes this method
	switch {
	case name == "conversation_list":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			Limit  int `json:"limit"`
			Offset int `json:"offset"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		currID := application.ConversationIDFromContext(ctx)
		count, items, err := t.Conversations.List(currID, limit, args.Offset)
		if err != nil {
			return "", err
		}
		rawItems := make([]any, len(items))
		for i, item := range items {
			rawItems[i] = item
		}
		return yamlJSONL(map[string]any{"count": count, "offset": args.Offset, "limit": limit}, rawItems), nil

	case name == "conversation_search":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			Query  string `json:"query"`
			ID     string `json:"id"`
			Limit  int    `json:"limit"`
			Offset int    `json:"offset"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		currID := application.ConversationIDFromContext(ctx)
		scopeID := strings.TrimSpace(args.ID)
		if scopeID != "" {
			count, items, err := t.Conversations.SearchMessages(scopeID, args.Query, limit, args.Offset)
			if err != nil {
				return "", err
			}
			rawItems := make([]any, len(items))
			for i, item := range items {
				rawItems[i] = item
			}
			return yamlJSONL(map[string]any{
				"count":  count,
				"offset": args.Offset,
				"limit":  limit,
				"query":  args.Query,
				"id":     scopeID,
				"scope":  "messages",
			}, rawItems), nil
		}
		count, items, err := t.Conversations.Search(currID, args.Query, limit, args.Offset)
		if err != nil {
			return "", err
		}
		rawItems := make([]any, len(items))
		for i, item := range items {
			rawItems[i] = item
		}
		return yamlJSONL(map[string]any{
			"count":  count,
			"offset": args.Offset,
			"limit":  limit,
			"query":  args.Query,
			"scope":  "rooms",
		}, rawItems), nil

	case name == "conversation_info":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID    string `json:"id"`
			Chunk *int   `json:"chunk"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required")
		}
		info, err := t.Conversations.Info(args.ID, args.Chunk)
		if err != nil {
			return "", err
		}
		return yamlBlock(info), nil

	case name == "conversation_read":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID    string `json:"id"`
			Chunk *int   `json:"chunk"`
			Start *int   `json:"start"`
			End   *int   `json:"end"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("id is required")
		}
		result, err := t.Conversations.Read(args.ID, args.Chunk, args.Start, args.End)
		if err != nil {
			return "", err
		}
		meta := map[string]any{
			"id":         result.ID,
			"start":      result.Start,
			"end":        result.End,
			"turn_count": result.TurnCount,
			"count":      len(result.Messages),
		}
		if result.ChunkIndex != nil {
			meta["chunk"] = *result.ChunkIndex
		}
		rawItems := make([]any, len(result.Messages))
		for i, item := range result.Messages {
			rawItems[i] = item
		}
		return capJSONL("conversation_read", meta, rawItems), nil

	case name == "conversation_send":
		if t.Conversations == nil {
			return "", depMissing("conversation service not available")
		}
		var args struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.ID) == "" {
			return "", fmt.Errorf("target conversation id is required")
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("message content is required")
		}
		currID := application.ConversationIDFromContext(ctx)
		if err := t.Conversations.Send(currID, args.ID, args.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("Message delivered to conversation `%s`", args.ID), nil

	case name == "skill_list":
		var args struct {
			Limit  int    `json:"limit"`
			Status string `json:"status"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 100
		}
		skills := filterSkills(t.skillsForContext(ctx), args.Status)
		if limit < len(skills) {
			skills = skills[:limit]
		}
		items := make([]any, 0, len(skills))
		for _, s := range skills {
			items = append(items, formatSkillJSON(s))
		}
		return yamlJSONL(map[string]any{"count": len(skills)}, items), nil

	case name == "skill_search":
		var args struct {
			Query  string `json:"query"`
			Limit  int    `json:"limit"`
			Status string `json:"status"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		if strings.TrimSpace(args.Query) == "" {
			return "", fmt.Errorf("query is required")
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		if t.SkillSearcher != nil {
			return t.searchSkillsRanked(ctx, args.Query, limit, args.Status)
		}
		q := strings.ToLower(args.Query)
		var items []any
		for _, s := range filterSkills(t.skillsForContext(ctx), args.Status) {
			if !strings.Contains(strings.ToLower(s.Name+" "+s.Description+" "+s.Content), q) {
				continue
			}
			items = append(items, formatSkillJSON(s))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case name == "skill_save":
		var args struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Path        string `json:"path"`
			Content     string `json:"content"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		name := strings.TrimSpace(args.Name)
		if name == "" {
			return "", fmt.Errorf("skill name is required")
		}
		if strings.TrimSpace(args.Content) == "" {
			return "", fmt.Errorf("skill content is required")
		}
		rel, support, err := domain.SkillSaveSupportPath(args.Path)
		if err != nil {
			return "", fmt.Errorf("skill save: %w", err)
		}
		if support {
			lookup := name
			if id := strings.TrimSpace(args.ID); id != "" {
				lookup = id
			}
			existing, err := t.skillForContext(ctx, lookup, "")
			if err != nil {
				return "", fmt.Errorf("skill save: skill %q not found; omit path to create SKILL.md, or pass the existing skill id to write a support file", lookup)
			}
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, lookup)
			}
			if t.Skills == nil {
				return "", depMissing("skill store not configured")
			}
			if err := t.Skills.WriteFile(lookup, "", rel, args.Content); err != nil {
				return "", fmt.Errorf("skill save: %w", err)
			}
			return yamlBlock(map[string]any{"status": "saved"}), nil
		}
		var s *domain.Skill
		if args.ID != "" {
			existing, err := t.skillForContext(ctx, args.ID, "")
			if err != nil {
				return "", fmt.Errorf("skill %q not found: %w", args.ID, err)
			}
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, args.ID)
			}
			s = existing
		} else if existing, err := t.skillForContext(ctx, name, ""); err == nil {
			if !existing.CanAgentMutate() {
				return "", skillMutationError(existing, name)
			}
			s = existing
		} else {
			s = &domain.Skill{
				Origin:        domain.SkillOriginLearned,
				Status:        domain.SkillStatusExperimental,
				OwnedBy:       string(domain.SkillOriginLearned),
				Version:       1,
				ActiveVersion: 1,
			}
		}
		s.Name = name
		s.Description = strings.TrimSpace(args.Description)
		s.Content = args.Content
		s.UpdatedAt = clock.NewTime().Time()
		if t.Skills == nil {
			return "", depMissing("skill store not configured")
		}
		if err := t.Skills.Save(s); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "saved", "id": s.ID}), nil

	case name == "skill_delete":
		var args struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("skill id is required")
		}
		skill, err := t.skillForContext(ctx, id, args.OwnedBy)
		if err != nil {
			return "", err
		}
		if isExternalSkill(skill) {
			return "", skillMutationError(skill, id)
		}
		if skill.Origin != domain.SkillOriginLearned || (skill.Status != domain.SkillStatusCandidate && skill.Status != domain.SkillStatusExperimental) {
			return "", fmt.Errorf("only learned candidate/experimental skills can be deleted")
		}
		if t.Skills == nil {
			return "", depMissing("skill store not configured")
		}
		if err := t.Skills.Delete(id, args.OwnedBy); err != nil {
			return "", err
		}
		return yamlBlock(map[string]any{"status": "deleted", "id": id}), nil

	case name == "memory_search":
		if t.MemoryRecords == nil {
			return "", depMissing("memory record store not configured")
		}
		var args struct {
			Query   string `json:"query"`
			Type    string `json:"type"`
			Status  string `json:"status"`
			Scope   string `json:"scope"`
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 20
		}
		filter := domain.MemorySearchFilter{
			Query:   args.Query,
			Type:    args.Type,
			Status:  args.Status,
			Scope:   args.Scope,
			Project: args.Project,
			Limit:   limit,
		}
		items := make([]any, 0)
		for _, rec := range t.MemoryRecords.List() {
			if !memoryRecordMatches(rec, filter) {
				continue
			}
			items = append(items, formatMemoryRecordJSON(rec))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case name == "memory_get":
		if t.MemoryRecords == nil {
			return "", depMissing("memory record store not configured")
		}
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		id := strings.TrimSpace(args.ID)
		if id == "" {
			return "", fmt.Errorf("id is required")
		}
		rec, err := t.MemoryRecords.Get(id)
		if err != nil {
			return "", err
		}
		return yamlBlock(formatMemoryRecordJSON(rec)), nil

	case name == "memory_list":
		if t.MemoryRecords == nil {
			return yamlBlock(map[string]any{"count": 0, "error": "memory record store not configured"}), nil
		}
		var args struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Scope   string `json:"scope"`
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		filter := domain.MemorySearchFilter{
			Type:    args.Type,
			Status:  args.Status,
			Scope:   args.Scope,
			Project: args.Project,
			Limit:   limit,
		}
		items := make([]any, 0)
		for _, rec := range t.MemoryRecords.List() {
			if !memoryRecordMatches(rec, filter) {
				continue
			}
			items = append(items, formatMemoryRecordJSON(rec))
			if len(items) >= limit {
				break
			}
		}
		return yamlJSONL(map[string]any{"count": len(items)}, items), nil

	case name == "docs_list":
		var args struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(argsJSON, &args)
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		metas := t.Docs.List()
		total := len(metas)
		if limit < len(metas) {
			metas = metas[:limit]
		}
		items := make([]any, 0, len(metas))
		for _, m := range metas {
			items = append(items, map[string]any{"id": m.ID, "title": m.Title, "path": m.Path})
		}
		return capJSONL("docs", map[string]any{"count": len(metas), "total": total, "limit": limit}, items), nil

	case name == "docs_search":
		var args struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 10
		}
		hits := t.Docs.Search(args.Query, limit)
		items := make([]any, 0, len(hits))
		for _, h := range hits {
			items = append(items, map[string]any{"id": h.ID, "title": h.Title, "path": h.Path, "snippet": h.Snippet})
		}
		return capJSONL("docs", map[string]any{"count": len(hits)}, items), nil

	case name == "docs_read":
		var args struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(argsJSON, &args); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
		doc, err := t.Docs.Read(args.ID)
		if err != nil {
			return "", fmt.Errorf("document %q not found; use docs with op=list or op=search first", args.ID)
		}
		return capToolOutput("docs", map[string]any{"title": doc.Title, "path": doc.Path}, doc.Content), nil

	case strings.HasPrefix(name, "memory_project_"):
		return t.executeProjectMemory(ctx, op, argsJSON)

	default:
		return "", fmt.Errorf("unknown %s op %q", name, op)
	}
}

// execRegistry lazily creates the background-process registry; safe on the
// zero-value Toolbox used by tests.
func (t *Toolbox) execRegistry() *execRegistry {
	t.execRegOnce.Do(func() { t.execReg = newExecRegistry() })
	return t.execReg
}

// Close terminates every managed background exec process. The composition
// root defers it so detached children never outlive the app.
func (t *Toolbox) Close() {
	t.execRegistry().close()
}

// ExecuteStreamed runs a tool while forwarding live output chunks to onChunk.
// Only tools that produce streaming output (exec) honor the callback today;
// every other tool executes exactly like Execute and ignores it. Returns the
// final combined output string and error, same contract as Execute.
func (t *Toolbox) ExecuteStreamed(ctx context.Context, name string, argsJSON []byte, onChunk func(string)) (string, error) {
	if name == "exec" {
		_, out, err := t.dispatchExec(ctx, name, argsJSON, onChunk)
		return out, err
	}
	return t.Execute(ctx, name, argsJSON)
}

func (t *Toolbox) Execute(ctx context.Context, name string, argsJSON []byte) (string, error) {
	// Native built-ins first: file CRUD and the exec island.
	if name == "exec" {
		_, out, err := t.dispatchExec(ctx, name, argsJSON, nil)
		return out, err
	}
	if strings.HasPrefix(name, "file_") || name == "grep" || name == "find_file" || name == "show" {
		ok, out, err := executeFileToolCtx(ctx, name, argsJSON)
		if !ok {
			return "", fmt.Errorf("unknown tool %q", name)
		}
		return out, err
	}
	// Dispatcher families (advertised roots: skill, memory, docs,
	// memory_project, automation, automation_schedule) are routed exclusively through executeFamily: resolving
	// the required `op` and reaching a family handler is impossible without
	// going through it. Retired per-op names fall through to the registry
	// lookup below and end up as the honest "unknown tool" error.
	if application.IsDispatchRoot(name) {
		return t.executeFamily(ctx, name, argsJSON)
	}
	for _, e := range t.toolRegistry() {
		if e.info.Name == name && e.handler != nil {
			return e.handler(ctx, argsJSON)
		}
	}
	return "", fmt.Errorf("unknown tool: %s", name)
}

func (t *Toolbox) skillsForContext(ctx context.Context) []*domain.Skill {
	if t.RuntimeSkills != nil {
		return t.RuntimeSkills.List(application.WorkspaceFromContext(ctx))
	}
	if t.Skills == nil {
		return nil
	}
	return t.Skills.List()
}

func (t *Toolbox) skillForContext(ctx context.Context, id, ownedBy string) (*domain.Skill, error) {
	if t.RuntimeSkills != nil {
		return t.RuntimeSkills.Get(application.WorkspaceFromContext(ctx), id, ownedBy)
	}
	if t.Skills == nil {
		return nil, depMissing("skill store not configured")
	}
	return t.Skills.Get(id, ownedBy)
}

func isExternalSkill(skill *domain.Skill) bool {
	if skill == nil {
		return false
	}
	switch skill.EffectiveOwnedBy() {
	case string(domain.SkillOriginWorkspace), string(domain.SkillOriginGlobal):
		return true
	default:
		return false
	}
}

func skillMutationError(skill *domain.Skill, id string) error {
	if isExternalSkill(skill) {
		return fmt.Errorf("%s skill %q is read-only; edit its source SKILL.md", skill.EffectiveOwnedBy(), id)
	}
	return fmt.Errorf("cannot mutate trusted curated skill %q", id)
}

// searchSkillsRanked runs the ranked skill search (BM25 + graph + recency,
// no embedding) and appends substring matches the ranker missed (plural or
// inflected forms) so recall never regresses below the plain matcher.
func (t *Toolbox) searchSkillsRanked(ctx context.Context, query string, limit int, status string) (string, error) {
	results, err := t.SkillSearcher.SearchSkills(ctx, query, limit)
	if err != nil {
		return "", fmt.Errorf("skill search: %w", err)
	}
	skills := filterSkills(t.skillsForContext(ctx), status)
	byKey := make(map[string]*domain.Skill, len(skills))
	for _, sk := range skills {
		byKey[sk.ID] = sk
		if _, ok := byKey[sk.Name]; !ok {
			byKey[sk.Name] = sk
		}
	}
	seen := make(map[string]bool, len(results))
	items := make([]any, 0, limit)
	for _, r := range results {
		sk := byKey[r.ID]
		if sk == nil || seen[sk.ID] {
			continue
		}
		seen[sk.ID] = true
		items = append(items, formatSkillJSON(sk))
		if len(items) >= limit {
			break
		}
	}
	if len(items) < limit {
		q := strings.ToLower(query)
		for _, sk := range skills {
			if len(items) >= limit {
				break
			}
			if seen[sk.ID] {
				continue
			}
			if strings.Contains(strings.ToLower(sk.Name+" "+sk.Description+" "+sk.Content), q) {
				seen[sk.ID] = true
				items = append(items, formatSkillJSON(sk))
			}
		}
	}
	return yamlJSONL(map[string]any{"count": len(items)}, items), nil
}

// ---- json schema helpers ----

func obj(typ string, properties map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": typ}
	if len(properties) > 0 {
		m["properties"] = properties
	} else if typ == "object" {
		// Some providers (e.g. Bedrock) reject object schemas without a
		// "properties" key. Emit an empty object to stay OpenAI-compatible.
		m["properties"] = map[string]any{}
	}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}

// freeObj builds a free-form object schema with a description and an empty
// properties map (Bedrock rejects object schemas without a "properties"
// key). It deliberately sets no additionalProperties key: true would
// recreate the open-object function-calling trap that made weak models emit
// {}, false would block strict-mode adoption later. Empty-argument
// regressions are caught by the mcp_call MISSING_ARGS guard instead.
func freeObj(desc string) map[string]any {
	return map[string]any{"type": "object", "description": desc, "properties": map[string]any{}}
}

// requiredSchemaFields returns the field names a tool's input schema marks
// as required, or nil when the schema is absent, invalid, or declares none.
func requiredSchemaFields(schema json.RawMessage) []string {
	var s struct {
		Required []string `json:"required"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Required) == 0 {
		return nil
	}
	return s.Required
}

func props(entries ...any) map[string]any {
	out := map[string]any{}
	for i := 0; i+1 < len(entries); i += 2 {
		name, _ := entries[i].(string)
		out[name] = entries[i+1]
	}
	return out
}

func str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func arr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// arrObj builds an array-of-objects JSON schema with the given item
// properties, required fields, and description.
func arrObj(desc string, properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":        "array",
		"description": desc,
		"items":       obj("object", properties, required...),
	}
}

// strEnum builds a string schema restricted to the given enum values.
func strEnum(desc string, values ...string) map[string]any {
	enums := make([]any, len(values))
	for i, v := range values {
		enums[i] = v
	}
	return map[string]any{"type": "string", "description": desc, "enum": enums}
}

// matchMCPTool was removed: mcp__<server>__<tool> names are no longer
// callable. The single MCP execution contract is mcp_call with a ref
// (<server>:<tool>) from mcp_search / tool_list.

// automationRunYAML is the agent-facing run snapshot for automation_wait /
// automation_run_status. WakeAt is formatted as RFC3339 (or omitted) so it is
// not stuffed into map[string]any as *time.Time — yaml.v3 panics on that.
func automationRunYAML(run *domain.WorkflowRun, extra map[string]any) map[string]any {
	out := map[string]any{
		"run_id":         run.ID,
		"status":         run.Status,
		"summary":        run.Summary(),
		"blocked_reason": run.BlockedReason,
	}
	if run.WakeAt != nil {
		out["wake_at"] = clock.NewTime(*run.WakeAt).Format(time.RFC3339)
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func (t *Toolbox) executeAutomation(ctx context.Context, name string, argsJSON []byte) (string, bool, error) {
	if t.Automation == nil {
		return "", true, depMissing("automation is not configured")
	}
	a := t.Automation
	var args map[string]any
	if err := json.Unmarshal(argsJSON, &args); err != nil || args == nil {
		if err == nil {
			err = fmt.Errorf("arguments must be a JSON object")
		}
		return "", true, fmt.Errorf("invalid automation arguments: %w", err)
	}
	str := func(k string) string {
		v, _ := args[k].(string)
		return v
	}
	encode := func(v any, err error) (string, bool, error) {
		if err != nil {
			return "", true, err
		}
		return yamlBlock(v), true, nil
	}
	switch name {
	case "automation_validate":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		raw := []byte(str("yaml"))
		r, _ := a.ValidateYAML(raw)
		return encode(r, nil)
	case "automation_run":
		async, _ := args["async"].(bool)
		id := str("workflow_id")
		if id == "" {
			return "", true, fmt.Errorf("workflow_id is required (use automation op=list to see available workflows)")
		}
		var run *domain.WorkflowRun
		var err error
		if async {
			run, err = a.RunWorkflowAsync(ctx, id, "agent")
		} else {
			run, err = a.RunWorkflow(ctx, id, "agent")
		}
		return encode(run, err)
	case "automation_wait":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		timeoutMs, _ := args["timeout_ms"].(float64)
		timeout := 5 * time.Minute
		if timeoutMs > 0 {
			timeout = time.Duration(timeoutMs) * time.Millisecond
		}
		if timeout > time.Hour {
			timeout = time.Hour
		}
		run, err := a.WaitRun(ctx, str("run_id"), timeout)
		if err != nil {
			return "", true, err
		}
		return encode(automationRunYAML(run, map[string]any{
			"timed_out": !run.Status.IsTerminal(),
		}), nil)
	case "automation_run_status":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		run, err := a.Runs.Get(ctx, str("run_id"))
		if err != nil {
			return "", true, err
		}
		return encode(automationRunYAML(run, nil), nil)
	case "automation_logs":
		jobID := strings.TrimSpace(str("job_id"))
		if jobID == "" {
			return "", true, fmt.Errorf("job_id is required")
		}
		after, _ := args["after"].(float64)
		limit, _ := args["limit"].(float64)
		if limit <= 0 {
			limit = 200
		}
		if limit > 2000 {
			limit = 2000
		}
		chunks, err := a.Logs.Read(ctx, jobID, uint64(after), int(limit))
		return encode(chunks, err)
	case "automation_cancel":
		if strings.TrimSpace(str("run_id")) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		return encode(map[string]bool{"ok": true}, a.Exec.Cancel(ctx, str("run_id")))
	case "automation_steer":
		runID := str("run_id")
		text := str("text")
		if strings.TrimSpace(runID) == "" {
			return "", true, fmt.Errorf("run_id is required")
		}
		if strings.TrimSpace(text) == "" {
			return "", true, fmt.Errorf("text is required")
		}
		run, err := a.Runs.Get(ctx, runID)
		if err != nil {
			return "", true, fmt.Errorf("run not found: %w", err)
		}
		var convID string
		for _, j := range run.Jobs {
			for _, s := range j.Steps {
				if s.Status == domain.StatusRunning && s.ConversationID != "" {
					convID = s.ConversationID
				}
			}
		}
		if convID == "" {
			return "", true, fmt.Errorf("no running agent step to steer")
		}
		if t.Steerer == nil {
			return "", true, depMissing("steer is not configured")
		}
		if err := t.Steerer.SteerHeadlessTurn(convID, text); err != nil {
			return "", true, err
		}
		return encode(map[string]any{"steered": true, "conversation_id": convID}, nil)
	case "automation_list":
		list, err := a.Workflows.List(ctx)
		if err != nil {
			return "", true, err
		}
		type row struct {
			ID, Name, Availability, Reason string
			Enabled                        bool
		}
		var out []row
		for _, w := range list {
			avail, reason := a.AvailabilityOf(ctx, w)
			out = append(out, row{ID: w.ID, Name: w.Name, Enabled: w.Enabled, Availability: avail, Reason: reason})
		}
		return encode(out, nil)
	case "automation_read":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		w, err := a.Workflows.Get(ctx, workflowID)
		if err != nil {
			return "", true, err
		}
		avail, reason := a.AvailabilityOf(ctx, w)
		caps := []any{}
		for _, name := range w.ReferencedCapabilities() {
			b, _ := a.Caps.Resolve(ctx, name, domain.DefaultAutoStart)
			caps = append(caps, b)
		}
		return encode(map[string]any{"workflow": w, "availability": avail, "reason": reason, "capabilities": caps}, nil)
	case "automation_create":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, err
		}
		if n := str("name"); n != "" {
			w.Name = n
		}
		w.Enabled = true
		if enabled, ok := args["enabled"].(bool); ok {
			w.Enabled = enabled
		}
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "automation_enable", "automation_disable":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		w, err := a.Workflows.Get(ctx, workflowID)
		if err != nil {
			return "", true, err
		}
		if name == "automation_enable" {
			err = a.Sched.EnableWorkflow(ctx, w)
		} else {
			err = a.Sched.DisableWorkflow(ctx, w)
		}
		return encode(map[string]any{"id": w.ID, "enabled": w.Enabled}, err)
	case "automation_delete":
		workflowID := strings.TrimSpace(str("workflow_id"))
		if workflowID == "" {
			return "", true, fmt.Errorf("workflow_id is required")
		}
		if a.Workflows == nil {
			return "", true, depMissing("workflow store not configured")
		}
		err := a.Workflows.Delete(ctx, workflowID)
		return encode(map[string]any{"status": "deleted", "workflow_id": workflowID}, err)
	case "automation_schedule_once":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		at, err := time.Parse(time.RFC3339, str("at"))
		if err != nil {
			return "", true, fmt.Errorf("at must be RFC3339")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, fmt.Errorf("invalid workflow: %w", err)
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		if w.Name == "" {
			w.Name = "once"
		}
		w.Enabled = true
		w.Triggers = []domain.Trigger{{ID: "t1", Kind: domain.TriggerOnce, Family: domain.FamilyOnce, At: &at}}
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	case "automation_schedule_every":
		if strings.TrimSpace(str("yaml")) == "" {
			return "", true, fmt.Errorf("yaml is required")
		}
		w, err := a.ParseDefinition(str("yaml"))
		if err != nil {
			return "", true, fmt.Errorf("invalid workflow: %w", err)
		}
		if w.Name == "" {
			w.Name = str("name")
		}
		cron := strings.TrimSpace(str("cron"))
		interval := strings.TrimSpace(str("interval"))
		if cron != "" && interval != "" {
			return "", true, fmt.Errorf("cron and interval are mutually exclusive")
		}
		tr := domain.Trigger{ID: "t1", Family: domain.FamilyEvery, Timezone: str("timezone")}
		if cron != "" {
			tr.Kind = domain.TriggerCron
			tr.Cron = cron
		} else {
			d, err := time.ParseDuration(interval)
			if err != nil {
				return "", true, fmt.Errorf("interval or cron is required")
			}
			tr.Kind = domain.TriggerInterval
			tr.Interval = d
		}
		w.Triggers = []domain.Trigger{tr}
		w.Enabled = true
		saved, r, err := a.SaveWorkflow(ctx, w)
		return encode(map[string]any{"workflow": saved, "validation": r}, err)
	default:
		return "", false, nil
	}
}

// formatSkillJSON renders a skill as discovery metadata for list/search.
func formatSkillJSON(s *domain.Skill) map[string]any {
	return map[string]any{
		"id":          s.ID,
		"name":        s.Name,
		"description": s.Description,
		"owned_by":    s.EffectiveOwnedBy(),
		"status":      string(s.Status),
		"path":        s.Path,
		"bundled":     s.Bundled,
	}
}

func filterSkills(skills []*domain.Skill, status string) []*domain.Skill {
	status = strings.TrimSpace(status)
	out := make([]*domain.Skill, 0, len(skills))
	for _, s := range skills {
		if s == nil {
			continue
		}
		if status != "" {
			if string(s.Status) == status {
				out = append(out, s)
			}
			continue
		}
		if s.Routable() {
			out = append(out, s)
		}
	}
	return out
}

func memoryRecordMatches(m *domain.MemoryRecord, filter domain.MemorySearchFilter) bool {
	return m.Matches(filter)
}

func formatMemoryRecordJSON(m *domain.MemoryRecord) map[string]any {
	out := map[string]any{
		"id":     m.ID,
		"type":   m.Type,
		"body":   m.Body,
		"status": m.Status,
		"scope":  m.Scope.Level,
	}
	if m.Subject != "" {
		out["subject"] = m.Subject
	}
	if m.Scope.Project != "" {
		out["project"] = m.Scope.Project
	}
	return out
}
