package application

import (
	"context"
	"path/filepath"

	"nusashell/application/conversation"
	"nusashell/application/learn"
	"nusashell/application/logs"
	"nusashell/application/media"
	"nusashell/application/memory"
	"nusashell/application/pets"
	"nusashell/application/plugins"
	"nusashell/application/provider"
	"nusashell/application/settings"
	"nusashell/application/skills"
	"nusashell/application/subagent"
	"nusashell/application/telemetry"
	"nusashell/application/tools"
	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) pluginService() *plugins.Service {
	if a.pluginSvc != nil {
		return a.pluginSvc
	}
	pluginDeps := plugins.Deps{
		Store:     a.Plugins,
		Installer: a.PluginInstaller,
		MCP:       a.MCPToolbox,
		Skills:    a.Skills,
		Log:       a.log,
	}
	if a.Automation != nil {
		pluginDeps.Caps = a.Automation.Caps
	}
	a.pluginSvc = plugins.New(pluginDeps)
	return a.pluginSvc
}

func (a *App) logsService() *logs.Service {
	if a.logsSvc != nil {
		return a.logsSvc
	}
	a.logsSvc = logs.New(logs.Deps{Store: a.Logs})
	return a.logsSvc
}

func (a *App) telemetryService() *telemetry.Service {
	if a.telemetrySvc != nil {
		return a.telemetrySvc
	}
	a.telemetrySvc = telemetry.New(telemetry.Deps{
		Conversations: a.Conversations,
		Providers:     a.Providers,
	})
	return a.telemetrySvc
}

func (a *App) settingsService() *settings.Service {
	return settings.New(settings.Deps{
		Store:       a.Settings,
		Credentials: a.Credentials,
		OnApplied:   a.onSettingsApplied,
	})
}

func (a *App) onSettingsApplied(oldUserPrompt string, next domain.Settings) {
	a.InvalidateLearningSearcher()
	if next.UserPrompt != oldUserPrompt {
		a.publishAnnouncementToAll(newAnnouncement(
			"config_changed",
			domain.AnnouncementConfigChangedArgs([]string{"user_prompt"}),
			domain.AnnouncementConfigChangedMessage([]string{"user_prompt"}),
		), "")
	}
}

func (a *App) handleSettingsGet() (any, *contracts.RPCError) {
	return a.settingsService().HandleGet()
}

func (a *App) handleSettingsSet(req contracts.SettingsSetRequest) (any, *contracts.RPCError) {
	return a.settingsService().HandleSet(req)
}

func settingsDTO(s domain.Settings) contracts.SettingsDTO {
	return settings.ToDTO(s)
}

func (a *App) skillsService() *skills.Service {
	return skills.New(skills.Deps{
		Store: a.Skills,
		Log:   a.log,
		Bus:   a.Bus,
		OnChanged: func(op string) {
			a.publishAnnouncementToAll(newAnnouncement(
				"skills_changed",
				domain.AnnouncementSkillsChangedArgs(op),
				domain.AnnouncementSkillsChangedMessage(),
			), "")
		},
		OnLife: func(op, id, status string) {
			a.emitSkillLifecycle(op, id, status, "")
		},
	})
}

func (a *App) handleSkillsList() (any, *contracts.RPCError) {
	return a.skillsService().HandleList()
}
func (a *App) handleSkillsRead(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleRead(req)
}
func (a *App) handleSkillsSave(req contracts.SkillSaveRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleSave(req)
}
func (a *App) handleSkillsDelete(req contracts.SkillIDRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleDelete(req)
}
func (a *App) handleSkillsFileRead(req contracts.SkillFileReadRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleFileRead(req)
}
func (a *App) handleSkillsInstall(req contracts.SkillInstallRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleInstall(req)
}
func (a *App) handleSkillsPromote(req contracts.SkillPromoteRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandlePromote(req)
}
func (a *App) handleSkillsRollback(req contracts.SkillRollbackRequest) (any, *contracts.RPCError) {
	return a.skillsService().HandleRollback(req)
}

func (a *App) petsDeps() pets.Deps {
	return pets.Deps{
		Installer: a.PetsInstaller,
		Settings:  a.Settings,
		Log:       a.log,
		Bus:       a.Bus,
		Go:        func(fn func()) { a.goSafe("pets", fn) },
	}
}

func (a *App) petsService() *pets.Service {
	if a.petsSvc != nil {
		return a.petsSvc
	}
	a.petsSvc = pets.New(a.petsDeps())
	return a.petsSvc
}

func (a *App) handlePetsStatus() (any, *contracts.RPCError) {
	return a.petsService().HandleStatus()
}
func (a *App) handlePetsInstallStart(req contracts.PetsInstallStartRequest) (any, *contracts.RPCError) {
	return a.petsService().HandleInstallStart(req)
}
func (a *App) handlePetsLaunch() (any, *contracts.RPCError) {
	return a.petsService().HandleLaunch()
}

func (a *App) PetsInstallRunning() bool {
	if a.petsSvc == nil {
		return false
	}
	return a.petsSvc.InstallRunning()
}

func (a *App) StartPetAutoLaunch(ctx context.Context) {
	if a.PetsInstaller == nil {
		return
	}
	a.autostartOnce.Do(func() {
		a.goSafe("pets-autostart", func() { a.petsService().AutoLaunch(ctx) })
	})
}

func (a *App) conversationService() *conversation.Service {
	return conversation.New(a.conversationDeps())
}

func (a *App) conversationDeps() conversation.Deps {
	d := conversation.Deps{
		Store:       a.Conversations,
		Todos:       a.Todos,
		Attachments: a.Attachments,
		Acp:         a.Acp,
		Log:         a.log,
		LockTurn: func(id string) func() {
			mu := a.conversationTurnLock(id)
			mu.Lock()
			return mu.Unlock
		},
		CancelRun: func(id string) {
			if run := a.activeRunForConversation(id); run != nil {
				run.Cancel()
			}
		},
		Announce: func(targetID, fromID, content string) {
			a.publishAnnouncement(targetID, newAnnouncement(
				"peer_message",
				domain.AnnouncementPeerMessageArgs(fromID),
				domain.AnnouncementPeerMessageMessage(fromID, content),
			))
		},
	}
	if a.DirectoryBrowser != nil {
		d.Browser = convDirBrowser{inner: a.DirectoryBrowser}
	}
	return d
}

type convDirBrowser struct {
	inner DirectoryBrowser
}

func (b convDirBrowser) ListDirs(ctx context.Context, path string) (conversation.DirListing, error) {
	listing, err := b.inner.ListDirs(ctx, path)
	if err != nil {
		return conversation.DirListing{}, err
	}
	return conversation.DirListing{
		Path:      listing.Path,
		Parent:    listing.Parent,
		Entries:   listing.Entries,
		Truncated: listing.Truncated,
	}, nil
}

func (b convDirBrowser) EnsureDir(ctx context.Context, path string) error {
	return b.inner.EnsureDir(ctx, path)
}

func (a *App) mediaDeps() media.Deps {
	return media.Deps{
		ImageGen:     a.ImageGeneratorFactory,
		SpeechSynth:  a.SpeechSynthesizerFactory,
		OfflineSynth: a.OfflineSynthesizer,
		SpeechSTT:    a.SpeechTranscriberFactory,
		OfflineSTT:   a.OfflineTranscriberFactory,
		VideoGen:     a.VideoGeneratorFactory,
		TTSInstaller: a.TTSInstaller,
		STTInstaller: a.STTInstaller,
		Attachments:  a.Attachments,
		Resolve:      a.resolveFallbackProvider,
		ProviderName: a.providerNameByID,
		Describe:     a.describeMediaAttachment,
		Settings:     a.Settings,
		Log:          a.log,
		Bus:          a.Bus,
		Go:           func(name string, fn func()) { a.goSafe(name, fn) },
		RetryDelay:   providerRetryDelay,
		WaitRetry:    a.waitForRetry,
	}
}

func (a *App) mediaService() *media.Service {
	if a.mediaSvc != nil {
		return a.mediaSvc
	}
	a.mediaSvc = media.New(a.mediaDeps())
	return a.mediaSvc
}

func (a *App) memoryDeps() memory.Deps {
	d := memory.Deps{
		Records: a.MemoryRecords,
		Ops:     a.LearningOps,
		User:    a.User,
		Agent:   a.Agent,
		OnChanged: func(tier, op string) {
			a.publishAnnouncementToAll(newAnnouncement(
				"memory_changed",
				domain.AnnouncementMemoryChangedArgs(tier, op),
				domain.AnnouncementMemoryChangedMessage(),
			), "")
		},
		OnRecordDeleted: func(id string) {
			a.pruneLearningEdges(id)
			a.InvalidateLearningSearcher()
		},
		Announce: func(conversationID, typ, args, msg string) {
			a.publishAnnouncement(conversationID, newAnnouncement(typ, args, msg))
		},
		PersistAnnounced: func(conversationID string, ids []string) error {
			repo, err := a.loadRepo(conversationID)
			if err != nil {
				return err
			}
			repo.Conversation().LastAnnouncedRecords = ids
			return repo.Save()
		},
	}
	if a.Bus != nil {
		d.Bus = a.Bus
	}
	return d
}

func (a *App) memoryService() *memory.Service {
	if a.memorySvc != nil {
		return a.memorySvc
	}
	a.memorySvc = memory.New(a.memoryDeps())
	return a.memorySvc
}

func (a *App) learnDeps() learn.Deps {
	d := learn.Deps{
		Experiences:     a.Experiences,
		Records:         a.MemoryRecords,
		Jobs:            a.LearningJobs,
		Edges:           a.LearningEdges,
		Skills:          a.Skills,
		User:            a.User,
		Conversations:   a.Conversations,
		Providers:       a.Providers,
		Credentials:     a.Credentials,
		Settings:        a.Settings,
		EmbedderFactory: a.EmbedderFactory,
		EmbeddingCache:  a.EmbeddingCache,
		NewKeywordIndex: newLearnKeywordIndex,
		Log:             a.log,
		Go:              func(name string, fn func()) { a.goSafe(name, fn) },
		DataDir:         a.DataDir,
		Trajectory:      a.Trajectory,
		WithWorkspace:   WithWorkspace,
		OnMemoryUpdated: a.emitMemoryUpdated,
		OnSkillChanged: func(op, id, status, conversationID string) {
			a.publishAnnouncementToAll(newAnnouncement(
				"skills_changed",
				domain.AnnouncementSkillsChangedArgs(op),
				domain.AnnouncementSkillsChangedMessage(),
			), "")
		},
	}
	if a.Bus != nil {
		d.Bus = a.Bus
	}
	if a.EmbeddingCache != nil {
		d.EmbeddingCache = a.EmbeddingCache
	}
	if a.MemoryRecords != nil {
		mem := a.memoryService()
		d.ApplyMemory = mem.Apply
		d.RejectMemory = mem.Reject
	}
	if a.learningTurn != nil {
		d.LearningTurn = func(ctx context.Context, model, prompt string) (string, string, error) {
			return a.learningTurn(ctx, AgentLearner, model, prompt)
		}
	}
	if a.Providers != nil && a.Factory != nil && a.Conversations != nil && a.runs != nil {
		d.Headless = learnerHeadless{a}
	}
	if a.Conversations != nil {
		d.PersistConversation = func(c *domain.Conversation) error {
			return bindConversation(a.Conversations, c).Save()
		}
		d.LockConversation = func(id string) func() {
			mu := a.conversationTurnLock(id)
			mu.Lock()
			return mu.Unlock
		}
		if loc, ok := a.Conversations.(ConversationFileLocator); ok {
			d.ConversationPath = loc.ConversationPath
		}
	}
	return d
}

func (a *App) learnService() *learn.Service {
	if a == nil {
		return nil
	}
	a.learnMu.Lock()
	defer a.learnMu.Unlock()
	if a.learnSvc != nil {
		return a.learnSvc
	}
	a.learnSvc = learn.New(a.learnDeps())
	return a.learnSvc
}

func (a *App) toolsDeps() tools.Deps {
	return tools.Deps{Docs: a.Docs}
}

func (a *App) toolsService() *tools.Service {
	if a.toolsSvc != nil {
		return a.toolsSvc
	}
	a.toolsSvc = tools.New(a.toolsDeps())
	return a.toolsSvc
}

func (a *App) subagentDeps() subagent.Deps {
	d := subagent.Deps{
		Agents:        a.AcpAgents,
		Runtime:       a.Acp,
		RunStorage:    a.AcpRunStorage,
		Conversations: a.Conversations,
		Todos:         a.Todos,
		Settings:      a.Settings,
		Log:           a.log,
		Go:            func(name string, fn func()) { a.goSafe(name, fn) },
		OnAgentsChanged: func() {
			a.publishAnnouncementToAll(newAnnouncement(
				"config_changed",
				domain.AnnouncementConfigChangedArgs([]string{"subagent"}),
				domain.AnnouncementConfigChangedMessage([]string{"subagent"}),
			), "")
		},
		TrackPending: a.trackPendingRun,
		DeliverRunDone: func(conversationID, runID string, complete func(cid string) error) {
			a.deliverRunDone(conversationID, pendingRunDone{RunID: runID, Complete: complete})
		},
		CompleteSubagent: a.completeSubagentRunLocked,
		CompleteDelegate: a.completeDelegateRunLocked,
		ResolveModel: func(string) (string, error) {
			p, bare, _, err := a.resolveHeadlessModel("")
			if err != nil {
				return "", err
			}
			return p.ID + ":" + bare, nil
		},
		Headless: func(ctx context.Context, prompt, model string, trust domain.TrustLevel, schema map[string]any, onUpdate func(string)) (map[string]any, string, error) {
			return a.runHeadlessTurnKindObserved(ctx, prompt, model, trust, schema, AgentDelegate, onUpdate)
		},
	}
	if a.Bus != nil {
		d.Bus = a.Bus
	}
	return d
}

func (a *App) subagentService() *subagent.Service {
	if a.subagentSvc != nil {
		return a.subagentSvc
	}
	a.subagentSvc = subagent.New(a.subagentDeps())
	return a.subagentSvc
}

func (a *App) providerService() *provider.Service {
	return provider.New(a.providerDeps())
}

func (a *App) providerDeps() provider.Deps {
	d := provider.Deps{
		Store:       a.Providers,
		Credentials: a.Credentials,
		Factory:     a.Factory,
		Catalog:     a.ModelCatalog,
		Log:         a.log,
		DataDir:     a.DataDir,
		OnConfigChanged: func() {
			a.publishAnnouncementToAll(newAnnouncement(
				"config_changed",
				domain.AnnouncementConfigChangedArgs([]string{"provider"}),
				domain.AnnouncementConfigChangedMessage([]string{"provider"}),
			), "")
		},
		OfflineTTS: func() []contracts.ModelDTO {
			return offlineTTSModels(a.TTSInstaller)
		},
	}
	if a.EmbeddingModelListerFactory != nil {
		d.EmbeddingListerFactory = func(p *domain.Provider) provider.EmbeddingLister {
			return a.EmbeddingModelListerFactory(p)
		}
	}
	if a.ImageModelListerFactory != nil {
		d.ImageListerFactory = func(p *domain.Provider) provider.ImageLister {
			return a.ImageModelListerFactory(p)
		}
	}
	if a.SpeechModelListerFactory != nil {
		d.SpeechListerFactory = func(p *domain.Provider) provider.SpeechLister {
			return a.SpeechModelListerFactory(p)
		}
	}
	if a.VideoModelListerFactory != nil {
		d.VideoListerFactory = func(p *domain.Provider) provider.VideoLister {
			return a.VideoModelListerFactory(p)
		}
	}
	return d
}

func (a *App) StartSettingsWatcher(ctx context.Context) {
	if a.Settings == nil || a.DataDir == "" {
		return
	}
	w := settings.NewWatcher(settings.WatcherDeps{
		DataDir: a.DataDir,
		Store:   a.Settings,
		Bus:     a.Bus,
		Log:     a.log,
	})
	a.goSafe("settings-watch", func() { w.Run(ctx) })
	a.log("info", "settings-watch", "watching %s for external edits (poll %s)", filepath.Join(a.DataDir, "config", "settings.json"), settings.WatchInterval)
}
