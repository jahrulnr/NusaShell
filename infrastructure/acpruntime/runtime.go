package acpruntime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/acpclient"
)

func New() *Runtime {
	return &Runtime{
		conns:             map[string]*pooledConn{},
		runs:              map[string]*liveRun{},
		PermissionTimeout: domain.DefaultAcpPermissionTimeout,
	}
}

type Runtime struct {
	mu    sync.Mutex
	conns map[string]*pooledConn
	runs  map[string]*liveRun

	OnUpdate     func(run *domain.AcpRun)
	OnPermission func(run *domain.AcpRun, req domain.AcpPermissionRequest)
	OnDone       func(run *domain.AcpRun)
	OnModeChange func(run *domain.AcpRun, source string)

	PermissionTimeout time.Duration
}

type pooledConn struct {
	key     string
	conn    *acpclient.Conn
	agent   *domain.AcpAgent
	runtime *Runtime
	cwd     string
}

type liveRun struct {
	mu           sync.Mutex
	run          *domain.AcpRun
	conn         *pooledConn
	prompting    bool
	permCh       chan acpclient.RequestPermissionResult
	permID       string
	sessionAllow bool
	done         chan struct{}
	closed       bool
}

func (rt *Runtime) SetCallbacks(
	onUpdate, onDone func(*domain.AcpRun),
	onPerm func(*domain.AcpRun, domain.AcpPermissionRequest),
	onMode func(*domain.AcpRun, string),
) {
	rt.OnUpdate = onUpdate
	rt.OnDone = onDone
	rt.OnPermission = onPerm
	rt.OnModeChange = onMode
}

func (rt *Runtime) initLocked() {
	if rt.conns == nil {
		rt.conns = map[string]*pooledConn{}
	}
	if rt.runs == nil {
		rt.runs = map[string]*liveRun{}
	}
}

func (rt *Runtime) Close() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.initLocked()
	for _, lr := range rt.runs {
		lr.finish(domain.AcpRunCancelled, "runtime closed", "")
	}
	for _, c := range rt.conns {
		c.conn.Close()
	}
	rt.conns = map[string]*pooledConn{}
	rt.runs = map[string]*liveRun{}
}

func (rt *Runtime) Probe(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error) {
	return rt.withThrowaway(ctx, agent, func(conn *acpclient.Conn) (domain.AcpAgent, error) {
		init, err := conn.Initialize(ctx)
		if err != nil {
			return *agent, err
		}
		applyInitialize(agent, init)
		return *agent, nil
	})
}

func (rt *Runtime) Authenticate(ctx context.Context, agent *domain.AcpAgent, methodID string) error {
	_, err := rt.withThrowaway(ctx, agent, func(conn *acpclient.Conn) (domain.AcpAgent, error) {
		if _, err := conn.Initialize(ctx); err != nil {
			return *agent, err
		}
		if err := conn.Authenticate(ctx, methodID); err != nil {
			return *agent, err
		}
		agent.AuthMethodID = methodID
		return *agent, nil
	})
	return err
}

func (rt *Runtime) RefreshCatalog(ctx context.Context, agent *domain.AcpAgent) (domain.AcpAgent, error) {
	return rt.withThrowaway(ctx, agent, func(conn *acpclient.Conn) (domain.AcpAgent, error) {
		init, err := conn.Initialize(ctx)
		if err != nil {
			return *agent, err
		}
		applyInitialize(agent, init)
		cwd := agent.DefaultWorkspace
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		sess, err := newSessionWithLazyAuth(ctx, conn, agent, cwd)
		if err != nil {
			return *agent, domain.WrapSessionAuthError(agent, err)
		}
		applySession(agent, sess)
		return *agent, nil
	})
}

// newSessionWithLazyAuth tries NewSession first and only calls Authenticate
// when the agent reports an auth-required error. This prevents agents that
// persist their own auth (e.g. Devin/Codex storing tokens in
// ~/.codex/auth.json) from re-triggering browser login on every new
// connection. When AuthMethodID is empty and NewSession fails, the error is
// returned for WrapSessionAuthError to upgrade.
func newSessionWithLazyAuth(ctx context.Context, conn *acpclient.Conn, agent *domain.AcpAgent, cwd string) (acpclient.NewSessionResult, error) {
	sess, err := conn.NewSession(ctx, cwd)
	if err == nil {
		return sess, nil
	}
	if agent.AuthMethodID == "" || !domain.IsSessionAuthRequired(err) {
		return sess, err
	}
	if err := conn.Authenticate(ctx, agent.AuthMethodID); err != nil {
		return acpclient.NewSessionResult{}, err
	}
	return conn.NewSession(ctx, cwd)
}

func (rt *Runtime) withThrowaway(ctx context.Context, agent *domain.AcpAgent, fn func(*acpclient.Conn) (domain.AcpAgent, error)) (domain.AcpAgent, error) {
	cwd := agent.DefaultWorkspace
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	conn, err := rt.dialAgent(ctx, agent, cwd, nil)
	if err != nil {
		return *agent, err
	}
	defer conn.Close()
	return fn(conn)
}

// dialAgent opens an ACP connection using the transport selected by
// agent.EffectiveTransport(): stdio (local subprocess) or remote (WebSocket
// to a cloud agent). handler receives incoming agent→client requests.
func (rt *Runtime) dialAgent(ctx context.Context, agent *domain.AcpAgent, cwd string, handler acpclient.Handler) (*acpclient.Conn, error) {
	if agent.EffectiveTransport() == domain.AcpTransportRemote {
		w, err := acpclient.DialRemote(ctx, agent.Command, nil)
		if err != nil {
			return nil, err
		}
		return acpclient.DialWire(ctx, w, handler)
	}
	env := append([]string{}, os.Environ()...)
	for k, v := range agent.Env {
		env = append(env, k+"="+v)
	}
	return acpclient.Dial(ctx, agent.Command, agent.Args, env, cwd, handler)
}

func applyInitialize(agent *domain.AcpAgent, init acpclient.InitializeResult) {
	agent.CachedCapabilities = domain.AcpCapabilities{
		LoadSession: init.AgentCapabilities.LoadSession,
		HasMCP:      init.AgentCapabilities.MCPCapabilities != nil,
	}
	agent.CachedAuthMethods = nil
	for _, m := range init.AuthMethods {
		agent.CachedAuthMethods = append(agent.CachedAuthMethods, domain.AcpAuthMethod{
			ID: m.ID, Name: m.Name, Description: m.Description,
		})
	}
}

func applySession(agent *domain.AcpAgent, sess acpclient.NewSessionResult) {
	if sess.Modes != nil && len(sess.Modes.AvailableModes) > 0 {
		agent.CachedCapabilities.HasModes = true
		agent.CachedModes = nil
		for _, m := range sess.Modes.AvailableModes {
			agent.CachedModes = append(agent.CachedModes, domain.AcpMode{ID: m.ID, Name: m.Name, Description: m.Description})
		}
		agent.ModeRiskMappings = domain.SeedModeRiskMappings(agent.CachedModes, agent.ModeRiskMappings)
	}
	if sess.Models != nil {
		agent.CachedModels = nil
		for _, m := range sess.Models.AvailableModels {
			agent.CachedModels = append(agent.CachedModels, domain.AcpModelInfo{
				ID: m.ModelID, Name: m.Name, Description: m.Description,
				Tier: domain.ClassifyModelTier(m.ModelID, m.Name),
			})
		}
	}
	applyConfigOptions(agent, sess.ConfigOptions)
}

// applyConfigOptions folds v1 session configuration selectors onto the
// legacy mode/model caches when the agent did not advertise those legacy
// payloads (OpenCode-generation agents return configOptions only). Filling
// gaps keeps agents that send both shapes deterministic. Switching still
// goes through legacy set_mode/set_model, which such agents keep serving.
func applyConfigOptions(agent *domain.AcpAgent, opts []acpclient.ConfigOption) {
	for _, opt := range opts {
		if !isSelectOption(opt) || len(opt.Options) == 0 {
			continue
		}
		switch opt.Category {
		case "mode":
			if len(agent.CachedModes) > 0 {
				continue
			}
			modes := make([]domain.AcpMode, 0, len(opt.Options))
			for _, choice := range opt.Options {
				modes = append(modes, domain.AcpMode{
					ID: choice.Value, Name: choice.Name, Description: choice.Description,
				})
			}
			agent.CachedCapabilities.HasModes = true
			agent.CachedModes = modes
			agent.ModeRiskMappings = domain.SeedModeRiskMappings(modes, agent.ModeRiskMappings)
		case "model":
			if len(agent.CachedModels) > 0 {
				continue
			}
			models := make([]domain.AcpModelInfo, 0, len(opt.Options))
			for _, choice := range opt.Options {
				models = append(models, domain.AcpModelInfo{
					ID: choice.Value, Name: choice.Name, Description: choice.Description,
					Tier: domain.ClassifyModelTier(choice.Value, choice.Name),
				})
			}
			agent.CachedModels = models
		}
	}
}

func isSelectOption(opt acpclient.ConfigOption) bool {
	return opt.Type == "" || opt.Type == "select"
}

// sessionConfigValue returns the current value of the select config option
// with the given category (e.g. "mode", "model"), or "".
func sessionConfigValue(opts []acpclient.ConfigOption, category string) string {
	for _, opt := range opts {
		if opt.Category == category && isSelectOption(opt) {
			return opt.StringValue()
		}
	}
	return ""
}

// sessionCurrentMode reads the session's current mode from either the
// legacy modes payload or v1 config options.
func sessionCurrentMode(sess acpclient.NewSessionResult) string {
	if sess.Modes != nil && sess.Modes.CurrentModeID != "" {
		return sess.Modes.CurrentModeID
	}
	return sessionConfigValue(sess.ConfigOptions, "mode")
}

// hasModeSelector reports whether the session advertises switchable modes
// through either the legacy modes payload or v1 config options.
func hasModeSelector(sess acpclient.NewSessionResult) bool {
	if sess.Modes != nil {
		return true
	}
	for _, opt := range sess.ConfigOptions {
		if opt.Category == "mode" && isSelectOption(opt) {
			return true
		}
	}
	return false
}

func (rt *Runtime) Spawn(ctx context.Context, req application.AcpSpawnRequest) (*domain.AcpRun, error) {
	if req.Agent == nil {
		return nil, fmt.Errorf("acp agent is required")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}
	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		workspace = strings.TrimSpace(req.Agent.DefaultWorkspace)
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}

	rt.mu.Lock()
	rt.initLocked()
	live := 0
	for _, r := range rt.runs {
		r.mu.Lock()
		if r.run.Live() {
			live++
		}
		r.mu.Unlock()
	}
	if live >= domain.MaxAcpConcurrentRuns {
		rt.mu.Unlock()
		return nil, fmt.Errorf("too many live ACP subagents (max %d)", domain.MaxAcpConcurrentRuns)
	}
	rt.mu.Unlock()

	pc, err := rt.ensureConn(req.Agent, workspace)
	if err != nil {
		return nil, err
	}
	sess, err := newSessionWithLazyAuth(ctx, pc.conn, req.Agent, workspace)
	if err != nil {
		return nil, domain.WrapSessionAuthError(req.Agent, err)
	}
	applySession(req.Agent, sess)

	modeID := strings.TrimSpace(req.ModeID)
	if modeID == "" {
		modeID = strings.TrimSpace(req.Agent.PreferredModeID)
	}
	if modeID == "" {
		modeID = domain.StrictestAvailableMode(req.Agent.CachedModes, req.Agent.ModeRiskMappings)
	}
	currentMode := sessionCurrentMode(sess)
	if modeID != "" && hasModeSelector(sess) && modeID != currentMode {
		if err := pc.conn.SetMode(ctx, sess.SessionID, modeID); err == nil {
			currentMode = modeID
		}
	}
	if currentMode != "" {
		modeID = currentMode
	}

	modelID := strings.TrimSpace(req.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(req.Agent.PreferredModelID)
	}
	modelStatus := domain.ModelSelectionNone
	currentModel := ""
	if sess.Models != nil {
		currentModel = sess.Models.CurrentModelID
	} else if cur := sessionConfigValue(sess.ConfigOptions, "model"); cur != "" {
		currentModel = cur
	}
	if modelID != "" {
		res, err := pc.conn.SetModel(ctx, sess.SessionID, modelID)
		if err != nil {
			modelStatus = domain.ModelSelectionRejected
		} else if res.Models == nil || res.Models.CurrentModelID == "" {
			modelStatus = domain.ModelSelectionUnverified
			currentModel = modelID
		} else if res.Models.CurrentModelID == modelID {
			modelStatus = domain.ModelSelectionConfirmed
			currentModel = modelID
		} else {
			modelStatus = domain.ModelSelectionRejected
			currentModel = res.Models.CurrentModelID
		}
	}

	now := time.Now().UTC()
	run := &domain.AcpRun{
		ID:                   domain.NewID("acprun"),
		AgentID:              req.Agent.ID,
		AgentName:            req.Agent.Name,
		ConversationID:       req.ConversationID,
		ParentToolCallID:     req.ParentToolCallID,
		SessionID:            sess.SessionID,
		Workspace:            workspace,
		Prompt:               req.Prompt,
		Status:               domain.AcpRunRunning,
		CurrentModeID:        modeID,
		AvailableModes:       req.Agent.CachedModes,
		CurrentModelID:       currentModel,
		ModelSelectionStatus: modelStatus,
		RiskTier:             domain.InferRiskTier(modeID, req.Agent.ModeRiskMappings),
		StartedAt:            now,
		UpdatedAt:            now,
	}
	lr := &liveRun{
		run:  run,
		conn: pc,
		done: make(chan struct{}),
	}
	rt.mu.Lock()
	rt.runs[run.ID] = lr
	rt.mu.Unlock()
	rt.emitUpdate(lr.snapshot())

	go lr.drivePrompt(req.Prompt)
	return lr.snapshot(), nil
}

func (rt *Runtime) ensureConn(agent *domain.AcpAgent, workspace string) (*pooledConn, error) {
	key := agent.ID + "\x00" + workspace
	rt.mu.Lock()
	rt.initLocked()
	if existing, ok := rt.conns[key]; ok {
		rt.mu.Unlock()
		return existing, nil
	}
	rt.mu.Unlock()

	pc := &pooledConn{key: key, agent: agent, runtime: rt, cwd: workspace}
	conn, err := rt.dialAgent(context.Background(), agent, workspace, pc)
	if err != nil {
		return nil, err
	}
	initCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	init, err := conn.Initialize(initCtx)
	cancel()
	if err != nil {
		conn.Close()
		return nil, err
	}
	applyInitialize(agent, init)
	// Lazy auth: do NOT proactively call Authenticate here. NewSession in
	// Spawn will try first and only Authenticate on auth-required failure.
	// This prevents agents that persist their own auth (Devin/Codex) from
	// re-triggering browser login on every pooled connection.
	pc.conn = conn
	rt.mu.Lock()
	rt.initLocked()
	if existing, ok := rt.conns[key]; ok {
		rt.mu.Unlock()
		conn.Close()
		return existing, nil
	}
	rt.conns[key] = pc
	rt.mu.Unlock()
	return pc, nil
}

func (pc *pooledConn) runBySession(sessionID string) *liveRun {
	pc.runtime.mu.Lock()
	defer pc.runtime.mu.Unlock()
	for _, lr := range pc.runtime.runs {
		if lr.run.SessionID == sessionID && lr.conn == pc {
			return lr
		}
	}
	return nil
}

func (pc *pooledConn) SessionUpdate(params acpclient.SessionUpdateParams) {
	lr := pc.runBySession(params.SessionID)
	if lr == nil {
		return
	}
	lr.mu.Lock()
	u := params.Update
	now := time.Now().UTC()
	var chunk domain.AcpTranscriptChunk
	switch u.SessionUpdate {
	case "agent_message_chunk", "agent_thought_chunk", "user_message_chunk":
		kind := "text"
		if u.SessionUpdate == "agent_thought_chunk" {
			kind = "thought"
		}
		text := ""
		if u.Content != nil {
			text = u.Content.Text
		}
		chunk = domain.AcpTranscriptChunk{Kind: kind, Text: text, At: now}
	case "tool_call", "tool_call_update":
		chunk = domain.AcpTranscriptChunk{
			Kind: "tool", ToolID: u.ToolCallID, ToolTitle: u.Title, ToolKind: u.Kind, ToolStatus: u.Status, At: now,
		}
	case "plan":
		var b strings.Builder
		for _, e := range u.Entries {
			b.WriteString(e.Status)
			b.WriteString(" ")
			b.WriteString(e.Content)
			b.WriteString("\n")
		}
		chunk = domain.AcpTranscriptChunk{Kind: "plan", Text: strings.TrimSpace(b.String()), At: now}
	case "usage_update":
		chunk = domain.AcpTranscriptChunk{Kind: "usage", Text: fmt.Sprintf("%d/%d", u.Used, u.Size), At: now}
	}
	if chunk.Kind != "" {
		lr.run.AppendTranscript(chunk)
	}
	if params.Update.SessionUpdate == "current_mode_update" && params.Update.CurrentModeID != "" {
		lr.run.CurrentModeID = params.Update.CurrentModeID
		lr.run.RiskTier = domain.InferRiskTier(params.Update.CurrentModeID, pc.agent.ModeRiskMappings)
		lr.run.UpdatedAt = time.Now().UTC()
		run := cloneRun(lr.run)
		lr.mu.Unlock()
		if pc.runtime.OnModeChange != nil {
			pc.runtime.OnModeChange(run, "agent")
		}
		pc.runtime.emitUpdate(run)
		return
	}
	lr.run.UpdatedAt = time.Now().UTC()
	run := cloneRun(lr.run)
	lr.mu.Unlock()
	pc.runtime.emitUpdate(run)
}

func (pc *pooledConn) RequestPermission(ctx context.Context, params acpclient.RequestPermissionParams) (acpclient.RequestPermissionResult, error) {
	lr := pc.runBySession(params.SessionID)
	if lr == nil {
		return acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "cancelled"}}, nil
	}
	paths := make([]string, 0, len(params.ToolCall.Locations))
	for _, loc := range params.ToolCall.Locations {
		if loc.Path != "" {
			paths = append(paths, loc.Path)
		}
	}
	lr.mu.Lock()
	tier := lr.run.RiskTier
	workspace := lr.run.Workspace
	lr.mu.Unlock()

	lr.mu.Lock()
	sessionAllow := lr.sessionAllow
	lr.mu.Unlock()
	if sessionAllow {
		slog.Info("acp permission auto-allow", "reason", "session_allow", "tool", params.ToolCall.Title, "session", params.SessionID)
		return acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "selected", OptionID: optionFor(params.Options, domain.PermissionAllowOnce)}}, nil
	}

	auto := domain.DecideAcpPermission(tier, params.ToolCall.Kind, paths, workspace)
	if auto.Auto {
		slog.Info("acp permission auto", "reason", auto.Reason, "tool", params.ToolCall.Title, "kind", params.ToolCall.Kind, "session", params.SessionID)
		return acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "selected", OptionID: optionFor(params.Options, auto.Outcome)}}, nil
	}

	// Orchestrator delegates authority: the parent agent spawned this
	// subagent with a task, so it implicitly authorizes the tools the
	// subagent needs. Auto-allow instead of prompting the user.
	slog.Info("acp permission auto-allow", "reason", "orchestrator_delegated", "tool", params.ToolCall.Title, "kind", params.ToolCall.Kind, "session", params.SessionID)
	return acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "selected", OptionID: optionFor(params.Options, domain.PermissionAllowOnce)}}, nil
}

func optionFor(opts []acpclient.PermissionOption, outcome domain.PermissionOutcome) string {
	want := "allow_once"
	if outcome == domain.PermissionDeny {
		want = "reject_once"
	}
	for _, o := range opts {
		if o.Kind == want {
			return o.OptionID
		}
	}
	if len(opts) > 0 {
		return opts[0].OptionID
	}
	return want
}

func optionIDForKinds(opts []domain.AcpPermissionOption, kinds ...string) string {
	for _, kind := range kinds {
		for _, o := range opts {
			if o.Kind == kind {
				return o.ID
			}
		}
	}
	if len(opts) > 0 {
		return opts[0].ID
	}
	return ""
}

func (pc *pooledConn) ReadTextFile(ctx context.Context, params acpclient.ReadTextFileParams) (acpclient.ReadTextFileResult, error) {
	path, err := containedPath(pc.cwd, params.Path)
	if err != nil {
		return acpclient.ReadTextFileResult{}, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return acpclient.ReadTextFileResult{}, err
	}
	content := string(b)
	if params.Line > 0 || params.Limit > 0 {
		lines := strings.Split(content, "\n")
		start := params.Line - 1
		if start < 0 {
			start = 0
		}
		if start > len(lines) {
			start = len(lines)
		}
		end := len(lines)
		if params.Limit > 0 && start+params.Limit < end {
			end = start + params.Limit
		}
		content = strings.Join(lines[start:end], "\n")
	}
	return acpclient.ReadTextFileResult{Content: content}, nil
}

func (pc *pooledConn) WriteTextFile(ctx context.Context, params acpclient.WriteTextFileParams) error {
	path, err := containedPath(pc.cwd, params.Path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(params.Content), 0o644)
}

func containedPath(workspace, p string) (string, error) {
	clean, ok := domain.ResolveWithinWorkspace(workspace, p)
	if !ok {
		return "", fmt.Errorf("path %q is outside the bound workspace", p)
	}
	return clean, nil
}

func (lr *liveRun) drivePrompt(text string) {
	lr.mu.Lock()
	if lr.closed {
		lr.mu.Unlock()
		return
	}
	lr.prompting = true
	sessionID := lr.run.SessionID
	lr.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := lr.conn.conn.Prompt(ctx, sessionID, text)

	lr.mu.Lock()
	lr.prompting = false
	steer := lr.run.QueuedSteer
	lr.run.QueuedSteer = ""
	if lr.closed {
		lr.mu.Unlock()
		return
	}
	if err != nil {
		status := domain.AcpRunFailed
		if strings.Contains(err.Error(), "exited") || strings.Contains(err.Error(), "closed") {
			status = domain.AcpRunFailed
		}
		lr.finishLocked(status, err.Error(), "")
		run := cloneRun(lr.run)
		lr.mu.Unlock()
		lr.conn.runtime.emitDone(run)
		return
	}
	if res.StopReason == "cancelled" {
		lr.finishLocked(domain.AcpRunCancelled, "", res.StopReason)
		run := cloneRun(lr.run)
		lr.mu.Unlock()
		lr.conn.runtime.emitDone(run)
		return
	}
	if steer != "" {
		lr.run.Status = domain.AcpRunRunning
		lr.run.UpdatedAt = time.Now().UTC()
		lr.mu.Unlock()
		lr.conn.runtime.emitUpdate(cloneRun(lr.run))
		lr.drivePrompt(steer)
		return
	}
	lr.finishLocked(domain.AcpRunCompleted, "", res.StopReason)
	run := cloneRun(lr.run)
	lr.mu.Unlock()
	lr.conn.runtime.emitDone(run)
}

func (lr *liveRun) finish(status domain.AcpRunStatus, errMsg, stop string) {
	lr.mu.Lock()
	lr.finishLocked(status, errMsg, stop)
	lr.mu.Unlock()
}

func (lr *liveRun) finishLocked(status domain.AcpRunStatus, errMsg, stop string) {
	if lr.closed {
		return
	}
	lr.closed = true
	lr.run.Status = status
	lr.run.Error = errMsg
	lr.run.StopReason = stop
	lr.run.PendingPermission = nil
	lr.run.EndedAt = time.Now().UTC()
	lr.run.UpdatedAt = lr.run.EndedAt
	if lr.permCh != nil {
		select {
		case lr.permCh <- acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "cancelled"}}:
		default:
		}
		lr.permCh = nil
	}
	close(lr.done)
}

func (rt *Runtime) Steer(runID, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("steer text is required")
	}
	lr, err := rt.live(runID)
	if err != nil {
		return err
	}
	startNow := false
	lr.mu.Lock()
	if !lr.run.Live() {
		lr.mu.Unlock()
		return fmt.Errorf("run is not active")
	}
	if lr.prompting {
		lr.run.QueuedSteer = text
		lr.run.UpdatedAt = time.Now().UTC()
		snap := cloneRun(lr.run)
		lr.mu.Unlock()
		rt.emitUpdate(snap)
		return nil
	}
	lr.run.Status = domain.AcpRunRunning
	lr.run.UpdatedAt = time.Now().UTC()
	startNow = true
	snap := cloneRun(lr.run)
	lr.mu.Unlock()
	rt.emitUpdate(snap)
	if startNow {
		go lr.drivePrompt(text)
	}
	return nil
}

func (rt *Runtime) Stop(runID string) error {
	lr, err := rt.live(runID)
	if err != nil {
		return err
	}
	_ = lr.conn.conn.Cancel(lr.run.SessionID)
	lr.finish(domain.AcpRunCancelled, "", "cancelled")
	rt.emitDone(lr.snapshot())
	return nil
}

func (rt *Runtime) Wait(ctx context.Context, runID string) (*domain.AcpRun, error) {
	lr, err := rt.live(runID)
	if err != nil {
		return nil, err
	}
	select {
	case <-lr.done:
		return lr.snapshot(), nil
	case <-ctx.Done():
		return lr.snapshot(), ctx.Err()
	}
}

func (rt *Runtime) Get(runID string) (*domain.AcpRun, bool) {
	rt.mu.Lock()
	lr, ok := rt.runs[runID]
	rt.mu.Unlock()
	if !ok {
		return nil, false
	}
	return lr.snapshot(), true
}

func (rt *Runtime) List(conversationID string) []*domain.AcpRun {
	rt.mu.Lock()
	lrs := make([]*liveRun, 0, len(rt.runs))
	for _, lr := range rt.runs {
		if conversationID != "" && lr.run.ConversationID != conversationID {
			continue
		}
		lrs = append(lrs, lr)
	}
	rt.mu.Unlock()
	out := make([]*domain.AcpRun, 0, len(lrs))
	for _, lr := range lrs {
		out = append(out, lr.snapshot())
	}
	return out
}

func (rt *Runtime) DecidePermission(runID, requestID, optionID string, outcome domain.PermissionOutcome) error {
	lr, err := rt.live(runID)
	if err != nil {
		return err
	}
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if lr.permCh == nil || (requestID != "" && lr.permID != requestID) {
		return fmt.Errorf("no pending permission for this run")
	}
	if outcome == domain.PermissionAllowSession {
		lr.sessionAllow = true
		if optionID == "" && lr.run.PendingPermission != nil {
			optionID = optionIDForKinds(lr.run.PendingPermission.Options, "allow_always", "allow_once")
		}
	}
	if optionID == "" && lr.run.PendingPermission != nil {
		switch outcome {
		case domain.PermissionAllowOnce:
			optionID = optionIDForKinds(lr.run.PendingPermission.Options, "allow_once")
		case domain.PermissionDeny:
			optionID = optionIDForKinds(lr.run.PendingPermission.Options, "reject_once", "reject_always")
		}
	}
	res := acpclient.RequestPermissionResult{Outcome: acpclient.PermissionOutcome{Outcome: "selected", OptionID: optionID}}
	if outcome == domain.PermissionCancelled || (outcome == domain.PermissionDeny && optionID == "") {
		res.Outcome = acpclient.PermissionOutcome{Outcome: "cancelled"}
	}
	select {
	case lr.permCh <- res:
	default:
	}
	lr.permCh = nil
	lr.permID = ""
	lr.run.PendingPermission = nil
	if lr.run.Status == domain.AcpRunWaitingPermission {
		lr.run.Status = domain.AcpRunRunning
	}
	lr.run.UpdatedAt = time.Now().UTC()
	rt.emitUpdate(cloneRun(lr.run))
	return nil
}

func (rt *Runtime) PromoteRisk(runID string, tier domain.RiskTier) error {
	if !domain.IsValidRiskTier(tier) {
		return fmt.Errorf("invalid risk tier")
	}
	lr, err := rt.live(runID)
	if err != nil {
		return err
	}
	lr.mu.Lock()
	defer lr.mu.Unlock()
	if !lr.run.Live() {
		return fmt.Errorf("run is not active")
	}
	lr.run.RiskTier = tier
	lr.run.UpdatedAt = time.Now().UTC()
	rt.emitUpdate(cloneRun(lr.run))
	return nil
}

func (rt *Runtime) SetMode(ctx context.Context, runID, modeID string) error {
	lr, err := rt.live(runID)
	if err != nil {
		return err
	}
	if err := lr.conn.conn.SetMode(ctx, lr.run.SessionID, modeID); err != nil {
		return err
	}
	lr.mu.Lock()
	lr.run.CurrentModeID = modeID
	lr.run.RiskTier = domain.InferRiskTier(modeID, lr.conn.agent.ModeRiskMappings)
	lr.run.UpdatedAt = time.Now().UTC()
	run := cloneRun(lr.run)
	lr.mu.Unlock()
	if rt.OnModeChange != nil {
		rt.OnModeChange(run, "user")
	}
	rt.emitUpdate(run)
	return nil
}

func (rt *Runtime) live(runID string) (*liveRun, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	lr, ok := rt.runs[runID]
	if !ok {
		return nil, fmt.Errorf("acp run %s not found", runID)
	}
	return lr, nil
}

func (rt *Runtime) emitUpdate(run *domain.AcpRun) {
	if rt.OnUpdate != nil {
		rt.OnUpdate(run)
	}
}

func (rt *Runtime) emitDone(run *domain.AcpRun) {
	if rt.OnDone != nil {
		rt.OnDone(run)
	} else {
		rt.emitUpdate(run)
	}
}

func (lr *liveRun) snapshot() *domain.AcpRun {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return cloneRun(lr.run)
}

func cloneRun(r *domain.AcpRun) *domain.AcpRun {
	if r == nil {
		return nil
	}
	cp := *r
	cp.Transcript = append([]domain.AcpTranscriptChunk(nil), r.Transcript...)
	cp.AvailableModes = append([]domain.AcpMode(nil), r.AvailableModes...)
	if r.PendingPermission != nil {
		p := *r.PendingPermission
		p.Paths = append([]string(nil), r.PendingPermission.Paths...)
		p.Options = append([]domain.AcpPermissionOption(nil), r.PendingPermission.Options...)
		cp.PendingPermission = &p
	}
	return &cp
}
