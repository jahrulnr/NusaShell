package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	appagent "github.com/jahrulnr/nusashell/application/agent"
	appauth "github.com/jahrulnr/nusashell/application/auth"
	appconversation "github.com/jahrulnr/nusashell/application/conversation"
	appjob "github.com/jahrulnr/nusashell/application/job"
	appmedia "github.com/jahrulnr/nusashell/application/media"
	appmemory "github.com/jahrulnr/nusashell/application/memory"
	appproject "github.com/jahrulnr/nusashell/application/project"
	"github.com/jahrulnr/nusashell/application/setting"
	appwebsearch "github.com/jahrulnr/nusashell/application/websearch"
	"github.com/jahrulnr/nusashell/infrastructure/config"
	"github.com/jahrulnr/nusashell/infrastructure/delivery/cli/admin"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	dservice "github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/redact"
	repoagent "github.com/jahrulnr/nusashell/infrastructure/repository/agent"
	repoauth "github.com/jahrulnr/nusashell/infrastructure/repository/auth"
	repoconfig "github.com/jahrulnr/nusashell/infrastructure/repository/config"
	repoconversation "github.com/jahrulnr/nusashell/infrastructure/repository/conversation"
	repofiletree "github.com/jahrulnr/nusashell/infrastructure/repository/filetree"
	repolog "github.com/jahrulnr/nusashell/infrastructure/repository/log"
	"github.com/jahrulnr/nusashell/infrastructure/repository/mediadescription"
	repomemory "github.com/jahrulnr/nusashell/infrastructure/repository/memory"
	repopick "github.com/jahrulnr/nusashell/infrastructure/repository/pick"
	repoproject "github.com/jahrulnr/nusashell/infrastructure/repository/project"
	repoprovider "github.com/jahrulnr/nusashell/infrastructure/repository/provider"
	reporuntime "github.com/jahrulnr/nusashell/infrastructure/repository/runtime"
	reposecret "github.com/jahrulnr/nusashell/infrastructure/repository/secret"
	reposkill "github.com/jahrulnr/nusashell/infrastructure/repository/skill"
	"github.com/jahrulnr/nusashell/infrastructure/repository/store"
	repoupload "github.com/jahrulnr/nusashell/infrastructure/repository/upload"
	repoworkspace "github.com/jahrulnr/nusashell/infrastructure/repository/workspace"
	serviceagent "github.com/jahrulnr/nusashell/infrastructure/service/agent"
	coreembedding "github.com/jahrulnr/nusashell/infrastructure/service/core/embedding"
	coreimage "github.com/jahrulnr/nusashell/infrastructure/service/core/image"
	coremedia "github.com/jahrulnr/nusashell/infrastructure/service/core/media"
	corespeech "github.com/jahrulnr/nusashell/infrastructure/service/core/speech"
	coretext "github.com/jahrulnr/nusashell/infrastructure/service/core/text"
	textcompletion "github.com/jahrulnr/nusashell/infrastructure/service/core/text/completion"
	textgeneratecontent "github.com/jahrulnr/nusashell/infrastructure/service/core/text/generatecontent"
	textmessages "github.com/jahrulnr/nusashell/infrastructure/service/core/text/messages"
	textresponses "github.com/jahrulnr/nusashell/infrastructure/service/core/text/responses"
	coretranscription "github.com/jahrulnr/nusashell/infrastructure/service/core/transcription"
	corevideo "github.com/jahrulnr/nusashell/infrastructure/service/core/video"
	"github.com/jahrulnr/nusashell/infrastructure/service/password"
	toolcustom "github.com/jahrulnr/nusashell/infrastructure/service/tool/custom"
	toolfile "github.com/jahrulnr/nusashell/infrastructure/service/tool/file"
	toolmcp "github.com/jahrulnr/nusashell/infrastructure/service/tool/mcp"
	toolmedia "github.com/jahrulnr/nusashell/infrastructure/service/tool/media"
	toolmemory "github.com/jahrulnr/nusashell/infrastructure/service/tool/memory"
	toolplan "github.com/jahrulnr/nusashell/infrastructure/service/tool/plan"
	toolproject "github.com/jahrulnr/nusashell/infrastructure/service/tool/project"
	toolskill "github.com/jahrulnr/nusashell/infrastructure/service/tool/skill"
	toolsummary "github.com/jahrulnr/nusashell/infrastructure/service/tool/summary"
	toolexec "github.com/jahrulnr/nusashell/infrastructure/service/tool/terminal"
	toolweb "github.com/jahrulnr/nusashell/infrastructure/service/tool/web"
	toolworkspace "github.com/jahrulnr/nusashell/infrastructure/service/tool/workspace"
	"github.com/jahrulnr/nusashell/infrastructure/service/wire"
	"github.com/jahrulnr/nusashell/migration"
	"github.com/jahrulnr/searchwire"
)

// engine is the wired application slice every front-end shares — the
// conversation broker over repositories, provider adapters, and tools,
// plus the diagnostic logger the adapters report to. The
// `serve` HTTP server composes one of these; providers and settings
// come from admin.LoadBoot.
type engine struct {
	conversations *appconversation.ConversationApp
	convs         drepo.Conversation
	agents        *appagent.AgentApp
	agentRuntime  dservice.AgentRuntime
	projects      *appproject.ProjectApp
	projectRepo   drepo.Project
	auth          *appauth.AuthApp
	// db is the application database; the caller closes it after the
	// conversation app has shut down.
	db     *sql.DB
	logger *repolog.Logger
	// storageDir is the resolved storage root, for stores composed later
	// (the WhatsApp device file).
	storageDir string
	// tools is the model runtime's tool catalog; the server adds the
	// built-in MCP servers' tools before it serves. toolOutputs keeps tool
	// files (images) per thread.
	tools       map[string]dservice.Tool
	toolOutputs drepo.ToolOutput
	// mcpTools offers the tools of the owner's MCP servers.
	mcpTools *toolmcp.Registry
	// planEvents is the registry a room's update_plan calls resolve their
	// live turn sink through; delivery binds one per running turn.
	planEvents *toolplan.Dispatcher
	// agentRepo resolves an agent's saved environment.
	agentRepo drepo.Agent
	// skills is the skill catalog reference search offers.
	skills drepo.Skills
	// uploads and picks keep what the owner attaches to a message: files,
	// and marks made on a thread's live page.
	uploads drepo.Uploads
	picks   drepo.Picks
	// memory remembers the owner and their work across threads; the
	// server serves it as the built-in "memory" MCP server.
	memory *appmemory.App
	// webSearch edits the web tools' settings.
	webSearch *appwebsearch.App
	// media runs the media roles; the WebUI transcribes voice input on it.
	media *appmedia.App
}

// compose wires repositories, provider adapters, the tool catalog, and
// the conversation broker from boot state. workspaceRoot is the
// delivery's default workspace — the root file tools, file://
// resolution, and the local driver's FallbackRoot run against when a
// room binds no workspace. A fallible step after the logger is created
// closes it (and the database) before returning, so an error return
// never leaks either. Caller owns
// Shutdown on the broker and Close on the logger.
func compose(cfg config.Config, secrets *reposecret.Store, configPath, workspaceRoot string) (*engine, error) {
	// An empty root would silently resolve to the process cwd inside
	// CheckRoot — exactly the accidental-coupling this param removes.
	if workspaceRoot == "" {
		return nil, errors.New("compose: workspace root is required")
	}
	dir, err := filepath.Abs(cfg.Storage.Dir)
	if err != nil {
		return nil, err
	}
	providers := repoprovider.NewProviderRepository(filepath.Join(dir, "providers"), secrets)
	convs := repoconversation.NewConversationRepository(filepath.Join(dir, "conversations"))
	tools := map[string]dservice.Tool{}
	workspaceStore := repoworkspace.NewLocal()
	home, err := repofiletree.NewLocal().HomeDir()
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := reporuntime.PrepareLaunchers(filepath.Join(dir, "runtimes"), executable); err != nil {
		return nil, err
	}
	commandEnv := toolexec.LocalEnvironment{Home: home, Storage: dir, Paths: cfg.Tools.Paths}
	customTools, err := toolcustom.NewTool(commandEnv)
	if err != nil {
		return nil, err
	}
	builtinDir := filepath.Join(dir, "builtin-skills")
	if err := reposkill.InstallBuiltins(builtinDir, toolskill.Builtins()); err != nil {
		return nil, err
	}
	skillStore := reposkill.NewLocal(home, builtinDir)
	skills, err := toolskill.NewSkillsTool(skillStore)
	if err != nil {
		return nil, err
	}
	readSkill, err := toolskill.NewReadSkillTool(skillStore)
	if err != nil {
		return nil, err
	}
	writeFile, err := toolfile.NewFileWriteTool(workspaceStore, workspaceRoot)
	if err != nil {
		return nil, err
	}
	patchFile, err := toolfile.NewFilePatchTool(workspaceStore, workspaceRoot)
	if err != nil {
		return nil, err
	}
	searchFiles, err := toolfile.NewFileSearchTool(workspaceStore, workspaceRoot)
	if err != nil {
		return nil, err
	}
	toolOutputs, err := repoconversation.NewToolOutputRepository(filepath.Join(dir, "conversations"))
	if err != nil {
		return nil, err
	}
	execTool, err := toolexec.NewExecTool(toolOutputs, toolexec.NewLocalRunner(commandEnv))
	if err != nil {
		return nil, err
	}
	// One terminal-session registry shared by every conversation — tools
	// resolve the room from ctx; the app reaps through it on delete and
	// shutdown.
	terminals := toolexec.NewSessionStore(commandEnv)
	execSession, err := toolexec.NewExecSessionTool(terminals)
	if err != nil {
		return nil, err
	}
	writeStdin, err := toolexec.NewWriteStdinTool(terminals)
	if err != nil {
		return nil, err
	}
	// The workspace record is a harness-side hydration tool: registering
	// it lets its name resolve in every room's catalog while
	// ToolPermissionSystem keeps it out of the provider's tool list.
	workspaceRecord, err := toolworkspace.NewWorkspaceTool()
	if err != nil {
		return nil, err
	}
	// project_context is the second harness-side hydration tool: the
	// environment runs it before each provider call and persists the
	// AGENTS.md + layout render as a deduped injection record.
	projectContext, err := toolproject.NewProjectContextTool(workspaceStore, workspaceRoot)
	if err != nil {
		return nil, err
	}
	// symbol_map is the third hydration record — registered only when the
	// binary carries the repomap tag (wire_symbolmap.go); the lean build
	// stub registers nothing.
	if err := wireSymbolMap(tools, configPath, dir, workspaceStore, workspaceRoot); err != nil {
		return nil, err
	}
	// The compaction checkpoint contract rides no room catalog — the
	// compaction job offers it alone inside its scratch dialogue.
	summaryTool, err := toolsummary.NewSummaryTool()
	if err != nil {
		return nil, err
	}
	// update_plan events reach the running turn's own event sink —
	// delivery binds one per room through this registry, the role
	// SessionStore plays for exec sessions.
	planEvents := toolplan.NewDispatcher()
	updatePlan, err := toolplan.NewUpdatePlanTool(planEvents)
	if err != nil {
		return nil, err
	}
	for _, tool := range []dservice.Tool{searchFiles, writeFile, patchFile, execTool, execSession, writeStdin, workspaceRecord, projectContext, skills, readSkill, customTools, updatePlan} {
		tools[tool.Schema().Name()] = tool
	}
	// Web search/fetch run locally through searchwire so the bound model
	// reasons over raw results itself — the answer tool is deliberately
	// skipped: it delegates answering to a provider-side model. The
	// conversation output store lets oversized fetches spill to readable
	// artifacts instead of eliding mid-document.
	// Their settings are read live: an edit in Settings or with `config
	// set` reaches the next call, and the master switch the next step.
	libDefaults := toolweb.LibraryDefaults
	webSearch := appwebsearch.New(appwebsearch.Deps{
		Config: func() (config.WebSearchConfig, error) {
			live := cfg
			if err := live.Load(repoconfig.NewLocal(), configPath); err != nil {
				return config.WebSearchConfig{}, err
			}
			return live.WebSearch, nil
		},
		Settings:  setting.New(repoconfig.NewLocal(), configPath),
		Secrets:   secrets,
		Providers: providers,
		Native:    toolweb.NativeSearch,
		Defaults: appwebsearch.Defaults{
			Limit: libDefaults.Limit, Timeout: libDefaults.Timeout, MaxResponseBytes: libDefaults.MaxResponseBytes,
			MaxRawBytes: libDefaults.MaxRawBytes, UserAgent: libDefaults.UserAgent,
		},
		Log: slog.Default(),
	})
	webTools, err := toolweb.Tools(toolOutputs, webSearch, searchwire.ToolSearch, searchwire.ToolFetch)
	if err != nil {
		return nil, err
	}
	for _, tool := range webTools {
		tools[tool.Schema().Name()] = tool
	}
	webSearch.UseTools(tools["web_"+searchwire.ToolSearch], tools["web_"+searchwire.ToolFetch])

	// The application database holds relational state (accounts and
	// sessions today). Opened before the logger so a failure leaks nothing.
	authMode, ok := enum.ParseAuthMode(cfg.Server.Auth)
	if !ok {
		return nil, fmt.Errorf("server.auth: unknown mode %q", cfg.Server.Auth)
	}
	db, err := store.Open(context.Background(), filepath.Join(dir, "nusashell.db"), migration.Files)
	if err != nil {
		return nil, err
	}
	auth, err := appauth.New(repoauth.New(db), password.Bcrypt{}, authMode, cfg.Server.Exposed())
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	logOpts := repolog.Options{MaxLines: cfg.Log.MaxLines, MaxFiles: cfg.Log.MaxFiles, Redact: redact.WithoutBodies}
	uploads, err := repoupload.New(filepath.Join(dir, "uploads"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	picks, err := repopick.New(filepath.Join(dir, "picks"))
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	logger, err := repolog.New(filepath.Join(dir, "logs"), logOpts)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	// One adapter instance per wire style — each is stateless and reads the
	// provider per Send, so the same instance serves every provider of a style.
	// The map is declared empty here and filled below, after the tool catalog
	// decides whether the omitted-attachment placeholder may name a reader;
	// the vision resolver closes over it and reads it per call.
	textOpts := coretext.Options{
		Title:         cfg.App.Name,
		Referer:       cfg.App.Source,
		Logger:        logger,
		HeaderTimeout: time.Duration(cfg.Service.HeaderTimeout),
		IdleTimeout:   time.Duration(cfg.Service.IdleTimeout),
		FileRoot:      workspaceRoot,
		FileStore:     workspaceStore,
	}
	services := make(map[enum.Style]dservice.TextModelService, 4)
	// Media tools run on the models assigned to the media roles in
	// Settings → Models, read from the file on every call so a change
	// applies without a restart.
	mediaClient := coremedia.New(coremedia.Options{
		Logger:        logger,
		Referer:       cfg.App.Source,
		Title:         cfg.App.Name,
		HeaderTimeout: time.Duration(cfg.Service.HeaderTimeout),
		IdleTimeout:   time.Duration(cfg.Service.IdleTimeout),
	})
	mediaModels := appmedia.New(providers, func() (appmedia.Roles, error) {
		live := cfg
		if err := live.Load(repoconfig.NewLocal(), configPath); err != nil {
			return appmedia.Roles{}, err
		}
		role := func(r config.ModelRole) appmedia.Assignment {
			return appmedia.Assignment{ProviderID: r.Provider, ModelID: r.Model}
		}
		m := live.Models
		return appmedia.Roles{
			Embedding: role(m.Embedding), Image: role(m.Image), Video: role(m.Video), Speech: role(m.Speech), Transcription: role(m.Transcription),
		}, nil
	}, appmedia.Adapters{
		Embedding:     coreembedding.New(mediaClient),
		Image:         coreimage.New(mediaClient),
		Speech:        corespeech.New(mediaClient),
		Transcription: coretranscription.New(mediaClient),
		Video:         corevideo.New(mediaClient, 0),
	})
	mediaTools, err := toolmedia.Tools(toolmedia.Deps{
		Models: mediaModels, Outputs: toolOutputs, Workspace: workspaceStore, Root: workspaceRoot,
	})
	if err != nil {
		_ = logger.Close()
		_ = db.Close()
		return nil, err
	}
	for _, tool := range mediaTools {
		tools[tool.Schema().Name()] = tool
	}
	// The file readers route by the turn model's capabilities: attach what it
	// can read, describe (or transcribe) what it cannot. The vision bridge
	// and the description store are read per call, so Settings changes apply
	// without a restart; the resolver loads what models.vision names and
	// picks the text adapter for that model's wire style — the same
	// (provider, model) → service resolution a turn uses.
	vision := dservice.VisionFuncs{
		Settings: func(context.Context) (dservice.VisionReading, error) {
			live := cfg
			if err := live.Load(repoconfig.NewLocal(), configPath); err != nil {
				return dservice.VisionReading{}, err
			}
			return dservice.VisionReading{
				ProviderID: live.Models.Vision.Provider,
				ModelID:    live.Models.Vision.Model,
				Consent:    live.Media.ReadingConsent,
			}, nil
		},
		Resolve: func(ctx context.Context, providerID, modelID string) (dservice.VisionTarget, error) {
			prov, err := providers.Load(ctx, providerID)
			if err != nil {
				return dservice.VisionTarget{}, err
			}
			model, ok := prov.Model(modelID)
			if !ok {
				return dservice.VisionTarget{}, fmt.Errorf(
					"the vision model %s is no longer in provider %s's catalog — choose another for models.vision", modelID, providerID)
			}
			style := model.WireStyle(prov.Style())
			svc, ok := services[style]
			if !ok {
				return dservice.VisionTarget{}, fmt.Errorf("no adapter for the vision model's wire style %s", style)
			}
			return dservice.VisionTarget{Provider: prov, Model: model, Service: svc}, nil
		},
	}
	// Descriptions live behind a repository, so a deleted room's text leaves
	// with the room: the tool writes it and the broker purges it.
	descriptions := mediadescription.New(filepath.Join(dir, "media-descriptions"))
	media := toolfile.MediaDeps{Descriptions: descriptions, Vision: vision, Transcriber: mediaModels}
	readMedia, err := toolfile.NewReadMediaTool(workspaceStore, workspaceRoot, media)
	if err != nil {
		_ = logger.Close()
		_ = db.Close()
		return nil, err
	}
	readFile, err := toolfile.NewFileReadTool(workspaceStore, workspaceRoot, media)
	if err != nil {
		_ = logger.Close()
		_ = db.Close()
		return nil, err
	}
	tools[readMedia.Schema().Name()] = readMedia
	tools[readFile.Schema().Name()] = readFile
	// The placeholder a model without the modality sees names the reader
	// that can actually help with that modality — and only when this build
	// registered it.
	var readers wire.Readers
	if _, ok := tools[toolfile.ReadMediaToolName]; ok {
		readers.Image = toolfile.ReadMediaToolName
	}
	if _, ok := tools[toolmedia.TranscribeToolName]; ok {
		readers.Audio = toolmedia.TranscribeToolName
	}
	textOpts.MediaReaders = readers
	services[enum.StyleCompletion] = textcompletion.New(textOpts)
	services[enum.StyleResponses] = textresponses.New(textOpts)
	services[enum.StyleMessages] = textmessages.New(textOpts)
	services[enum.StyleGenerateContent] = textgeneratecontent.New(textOpts)
	// Memory is read live too: turning it off in Settings stops the index
	// and the tools at the next step. Memory search embeds with the
	// embedding role when one is assigned, and falls back to full text.
	liveMemory := func() config.MemoryConfig {
		live := cfg
		if err := live.Load(repoconfig.NewLocal(), configPath); err != nil {
			return cfg.Memory
		}
		return live.Memory
	}
	memory := appmemory.New(appmemory.Deps{
		Memories: repomemory.New(db), Proposals: repomemory.NewProposals(db), Rooms: convs, Embed: mediaModels,
		Redact:        redact.String,
		Enabled:       func() bool { return liveMemory().On() },
		Consolidation: func() string { return liveMemory().Consolidation },
		Log:           slog.Default(),
	})
	memoryIndex, err := toolmemory.NewIndexTool(memory)
	if err != nil {
		_ = logger.Close()
		_ = db.Close()
		return nil, err
	}
	tools[memoryIndex.Schema().Name()] = memoryIndex
	mcpTools := toolmcp.NewRegistry(toolOutputs, admin.CLIName, version, nil)
	environments := map[enum.Environment]appjob.EnvironmentDriver{
		enum.EnvironmentLocal: localEnvironment(workspaceRoot, workspaceStore, services, tools, mcpTools),
	}

	// External agents run as child processes speaking ACP; one runtime
	// serves every room bound to an agent.
	agentRepo := repoagent.New(filepath.Join(dir, "agents"), secrets)
	agentRuntime := serviceagent.New(serviceagent.Options{
		ClientName:       admin.CLIName,
		ClientTitle:      admin.ProductName,
		ClientVersion:    version,
		Workspace:        workspaceStore,
		BuiltinSkillsDir: builtinDir,
		Uploads:          uploads,
		Catalog:          agentRepo,
	})

	projectRepo := repoproject.New(db)
	conversations := appconversation.New(convs, providers, environments, cfg, repoconfig.NewLocal(), configPath, terminals, summaryTool,
		appconversation.WithAgents(agentRepo, agentRepo, agentRuntime),
		appconversation.WithProjects(projectRepo),
		appconversation.WithUploads(uploads, picks))
	conversations.UseMediaDescriptions(descriptions)
	// A session the agent opted in to report ends here: its exit becomes a record
	// the room reads, and — while idle and under the cap — a turn that reads it.
	terminals.OnExit(conversations.OnCompletion)
	return &engine{
		conversations: conversations,
		convs:         convs,
		projects:      appproject.New(projectRepo, workspaceStore, conversations),
		projectRepo:   projectRepo,
		agents:        appagent.New(agentRepo, agentRuntime, serviceagent.Detector{}, slog.Default()),
		agentRuntime:  agentRuntime,
		auth:          auth,
		db:            db,
		storageDir:    dir,
		uploads:       uploads,
		picks:         picks,
		logger:        logger,
		tools:         tools,
		toolOutputs:   toolOutputs,
		mcpTools:      mcpTools,
		planEvents:    planEvents,
		agentRepo:     agentRepo,
		skills:        skillStore,
		memory:        memory,
		webSearch:     webSearch,
		media:         mediaModels,
	}, nil
}

// localEnvironment bundles the host-machine driver: the workspace repo,
// provider adapters, and tools all default to the delivery's workspace
// root, so a room with no bound workspace runs there.
func localEnvironment(root string, workspaceStore drepo.Workspace, services map[enum.Style]dservice.TextModelService, tools map[string]dservice.Tool, extra appjob.ToolSource) appjob.EnvironmentBundle {
	return appjob.EnvironmentBundle{
		WorkspaceStore: workspaceStore,
		DefaultRoot:    root,
		Services:       services,
		Tools:          tools,
		Extra:          extra,
	}
}
