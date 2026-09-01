package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/contracts"
	"nusashell/domain"
)

func (a *App) handleSettingsGet() (any, *contracts.RPCError) {
	return contracts.SettingsGetResult{Settings: settingsDTO(a.Settings.Get())}, nil
}

func (a *App) handleSettingsSet(req contracts.SettingsSetRequest) (any, *contracts.RPCError) {
	s := a.Settings.Get()
	oldUserPrompt := s.UserPrompt
	if req.CompactionEnabled != nil {
		s.CompactionEnabled = *req.CompactionEnabled
	}
	if req.CompactionThreshold != nil {
		if *req.CompactionThreshold < 0 || *req.CompactionThreshold > 2000000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction threshold must be between 0 and 2,000,000 (0 = auto)"}
		}
		s.CompactionThreshold = *req.CompactionThreshold
	}
	if req.CompactionModel != nil {
		s.CompactionModel = strings.TrimSpace(*req.CompactionModel)
	}
	if req.CompactionSummaryMaxTokens != nil {
		if *req.CompactionSummaryMaxTokens < 0 || *req.CompactionSummaryMaxTokens > 100000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction summary max tokens must be between 0 and 100000 (0 = default)"}
		}
		s.CompactionSummaryMaxTokens = *req.CompactionSummaryMaxTokens
	}
	if req.CompactionSummaryMinChars != nil {
		if *req.CompactionSummaryMinChars < 0 || *req.CompactionSummaryMinChars > 100000 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "compaction summary min chars must be between 0 and 100000 (0 = default)"}
		}
		s.CompactionSummaryMinChars = *req.CompactionSummaryMinChars
	}
	if req.ReviewModel != nil {
		s.ReviewModel = strings.TrimSpace(*req.ReviewModel)
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
	if req.RepeatedToolLimit != nil {
		if *req.RepeatedToolLimit < 0 || *req.RepeatedToolLimit > 100 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "repeated tool limit must be between 0 and 100 (0 = disabled)"}
		}
		s.RepeatedToolLimit = *req.RepeatedToolLimit
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
	if req.MaxParallelTools != nil {
		if *req.MaxParallelTools < 1 || *req.MaxParallelTools > 64 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max parallel tools must be between 1 and 64"}
		}
		s.MaxParallelTools = *req.MaxParallelTools
	}
	// Sampling parameters use json.RawMessage to distinguish three states:
	// absent (don't change), null (clear to nil), value (set). A *float64
	// with omitempty cannot tell null from absent, so once set the parameter
	// could never be cleared.
	if err := domain.ApplyOptionalFloat(req.Temperature, func(v float64) error {
		if v < 0 || v > 2 {
			return fmt.Errorf("temperature must be between 0 and 2")
		}
		return nil
	}, &s.Temperature); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := domain.ApplyOptionalFloat(req.TopP, func(v float64) error {
		if v < 0 || v > 1 {
			return fmt.Errorf("top_p must be between 0 and 1")
		}
		return nil
	}, &s.TopP); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := domain.ApplyOptionalInt(req.TopK, func(v int) error {
		if v < 1 {
			return fmt.Errorf("top_k must be at least 1")
		}
		return nil
	}, &s.TopK); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := domain.ApplyOptionalFloat(req.FrequencyPenalty, func(v float64) error {
		if v < -2 || v > 2 {
			return fmt.Errorf("frequency_penalty must be between -2 and 2")
		}
		return nil
	}, &s.FrequencyPenalty); err != nil {
		return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: err.Error()}
	}
	if err := domain.ApplyOptionalFloat(req.PresencePenalty, func(v float64) error {
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
	if req.AudioProviderID != nil {
		s.AudioProviderID = strings.TrimSpace(*req.AudioProviderID)
	}
	if req.AudioModelID != nil {
		s.AudioModelID = strings.TrimSpace(*req.AudioModelID)
	}
	if req.STTOfflineModel != nil {
		s.STTOfflineModel = strings.TrimSpace(*req.STTOfflineModel)
	}
	if req.STTOfflineLanguage != nil {
		s.STTOfflineLanguage = strings.TrimSpace(*req.STTOfflineLanguage)
	}
	if req.VideoProviderID != nil {
		s.VideoProviderID = strings.TrimSpace(*req.VideoProviderID)
	}
	if req.VideoModelID != nil {
		s.VideoModelID = strings.TrimSpace(*req.VideoModelID)
	}
	if req.TTSProviderID != nil {
		s.TTSProviderID = strings.TrimSpace(*req.TTSProviderID)
	}
	if req.TTSModelID != nil {
		s.TTSModelID = strings.TrimSpace(*req.TTSModelID)
	}
	if req.ImageProviderID != nil {
		s.ImageProviderID = strings.TrimSpace(*req.ImageProviderID)
	}
	if req.ImageModelID != nil {
		s.ImageModelID = strings.TrimSpace(*req.ImageModelID)
	}
	if req.VideoGenProviderID != nil {
		s.VideoGenProviderID = strings.TrimSpace(*req.VideoGenProviderID)
	}
	if req.VideoGenModelID != nil {
		s.VideoGenModelID = strings.TrimSpace(*req.VideoGenModelID)
	}
	if req.WebAnswerProvider != nil {
		s.WebAnswerProvider = strings.TrimSpace(*req.WebAnswerProvider)
	}
	if req.WebAnswerModel != nil {
		s.WebAnswerModel = strings.TrimSpace(*req.WebAnswerModel)
	}
	if req.WebAnswerAPIKey != nil {
		key := strings.TrimSpace(*req.WebAnswerAPIKey)
		if key == "" {
			_ = a.Credentials.Delete("web_answer")
		} else {
			if err := a.Credentials.Set("web_answer", key); err != nil {
				return nil, rpcInternal(err)
			}
		}
	}
	if req.WebSearchStrategy != nil {
		strategy := strings.TrimSpace(*req.WebSearchStrategy)
		if !domain.ValidWebSearchStrategy(strategy) {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "web_search_strategy must be auto, round_robin, random, or one of: brave, serper, tavily, startpage, wikipedia, github"}
		}
		s.WebSearchStrategy = strategy
	}
	// Web search provider API keys are write-only: they live in the
	// credential store (web_search_brave / _serper / _tavily), never in
	// settings JSON. An empty value clears the stored key.
	if req.WebSearchBraveAPIKey != nil {
		if err := setWebSearchCredential(a.Credentials, "web_search_brave", *req.WebSearchBraveAPIKey); err != nil {
			return nil, rpcInternal(err)
		}
	}
	if req.WebSearchSerperAPIKey != nil {
		if err := setWebSearchCredential(a.Credentials, "web_search_serper", *req.WebSearchSerperAPIKey); err != nil {
			return nil, rpcInternal(err)
		}
	}
	if req.WebSearchTavilyAPIKey != nil {
		if err := setWebSearchCredential(a.Credentials, "web_search_tavily", *req.WebSearchTavilyAPIKey); err != nil {
			return nil, rpcInternal(err)
		}
	}
	if req.PluginContractMode != nil {
		mode := strings.TrimSpace(*req.PluginContractMode)
		switch mode {
		case domain.PluginContractOff, domain.PluginContractHint, domain.PluginContractRequire:
			s.PluginContractMode = mode
		case "":
			// Reset to "follow the factory default" (anti-stamping: stored
			// empty resolves at runtime via contractMode()).
			s.PluginContractMode = ""
		default:
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "plugin_contract_mode must be off, hint, or require"}
		}
	}
	if req.LearningReviewThreshold != nil {
		v := *req.LearningReviewThreshold
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "learning_review_threshold must be >= 0 (0 disables turn-based review)"}
		}
		s.LearningReviewThreshold = v
	}
	if req.SkillNudgeInterval != nil {
		v := *req.SkillNudgeInterval
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "skill_nudge_interval must be >= 0 (0 disables tool-based review)"}
		}
		s.SkillNudgeInterval = v
	}
	if req.MaxAutoContinues != nil {
		v := *req.MaxAutoContinues
		if v < 0 {
			return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "max_auto_continues must be >= 0 (0 = unlimited)"}
		}
		s.MaxAutoContinues = v
	}
	if req.SoundNotifications != nil {
		s.SoundNotifications = *req.SoundNotifications
	}
	if req.UserPrompt != nil {
		s.UserPrompt = strings.TrimSpace(*req.UserPrompt)
	}
	if req.ProjectMemoryBase != nil {
		raw := strings.TrimSpace(*req.ProjectMemoryBase)
		if raw == "" {
			s.ProjectMemoryBase = ""
		} else {
			expanded := os.ExpandEnv(raw)
			expanded, err := domain.ExpandHomeDir(expanded)
			if err != nil {
				return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "project_memory_base: " + err.Error()}
			}
			if !filepath.IsAbs(expanded) {
				return nil, &contracts.RPCError{Code: contracts.CodeValidation, Message: "project_memory_base must be an absolute directory"}
			}
			s.ProjectMemoryBase = expanded
		}
	}
	if err := a.Settings.Set(s); err != nil {
		return nil, rpcInternal(err)
	}
	// Invalidate the learning searcher so the next search rebuilds it with
	// the new embedding settings (if the embedding model selection changed).
	a.InvalidateLearningSearcher()
	// The user instructions are appended to the system prompt: a real change
	// invalidates the cached system block for every conversation.
	if req.UserPrompt != nil && s.UserPrompt != oldUserPrompt {
		a.publishAnnouncementToAll(newAnnouncement(
			"config_changed",
			domain.AnnouncementConfigChangedArgs([]string{"user_prompt"}),
			domain.AnnouncementConfigChangedMessage([]string{"user_prompt"}),
		), "")
	}
	return contracts.SettingsGetResult{Settings: settingsDTO(s)}, nil
}

// setWebSearchCredential upserts or clears one web search provider API
// key in the credential store. Empty values delete the stored key so the
// provider falls back to its environment variable.
func setWebSearchCredential(creds CredentialStore, id, raw string) error {
	key := strings.TrimSpace(raw)
	if key == "" {
		return creds.Delete(id)
	}
	return creds.Set(id, key)
}

func settingsDTO(s domain.Settings) contracts.SettingsDTO {
	return contracts.SettingsDTO{
		CompactionEnabled:          s.CompactionEnabled,
		CompactionThreshold:        s.CompactionThreshold,
		CompactionModel:            s.CompactionModel,
		CompactionSummaryMaxTokens: s.CompactionSummaryMaxTokens,
		CompactionSummaryMinChars:  s.CompactionSummaryMinChars,
		ReviewModel:                s.ReviewModel,
		PromptCaching:              s.PromptCaching,
		MaxToolRounds:              s.MaxToolRounds,
		RepeatedToolLimit:          s.RepeatedToolLimit,
		MaxInputTokens:             s.MaxInputTokens,
		MaxOutputTokens:            s.MaxOutputTokens,
		MaxParallelTools:           s.MaxParallelTools,
		EmbeddingProviderID:        s.EmbeddingProviderID,
		EmbeddingModelID:           s.EmbeddingModelID,
		VisionProviderID:           s.VisionProviderID,
		VisionModelID:              s.VisionModelID,
		AudioProviderID:            s.AudioProviderID,
		AudioModelID:               s.AudioModelID,
		VideoProviderID:            s.VideoProviderID,
		VideoModelID:               s.VideoModelID,
		TTSProviderID:              s.TTSProviderID,
		TTSModelID:                 s.TTSModelID,
		ImageProviderID:            s.ImageProviderID,
		ImageModelID:               s.ImageModelID,
		VideoGenProviderID:         s.VideoGenProviderID,
		VideoGenModelID:            s.VideoGenModelID,
		WebAnswerProvider:          s.WebAnswerProvider,
		WebAnswerModel:             s.WebAnswerModel,
		WebSearchStrategy:          s.WebSearchStrategy,
		PluginContractMode:         s.PluginContractMode,
		Temperature:                s.Temperature,
		TopP:                       s.TopP,
		TopK:                       s.TopK,
		FrequencyPenalty:           s.FrequencyPenalty,
		PresencePenalty:            s.PresencePenalty,
		LearningReviewThreshold:    s.LearningReviewThreshold,
		SkillNudgeInterval:         s.SkillNudgeInterval,
		MaxAutoContinues:           s.MaxAutoContinues,
		SoundNotifications:         s.SoundNotifications,
		UserPrompt:                 s.UserPrompt,
		ProjectMemoryBase:          s.ProjectMemoryBase,
	}
}
