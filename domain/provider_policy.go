package domain

// ModelCapabilities describes which input modalities the active chat model
// supports. Unknown models (not in catalog) default to Vision=true (common
// across modern multimodal models) but Audio=false, Video=false, and
// Document=false (rare capabilities that cause provider errors when sent to
// models that lack them — e.g. Nvidia rejects audio data URLs with a
// confusing "Failed to load image" error; non-document models may silently
// drop PDF attachments). Users can add a model to the catalog with
// Audio=true, Video=true, or Document=true to enable native input for that
// model. Note: Document (PDF) support is separate from Vision — many vision
// models (Llama, Qwen, Grok, Mistral) cannot read PDFs.
type ModelCapabilities struct {
	Vision   bool // image input
	Audio    bool // audio input
	Video    bool // video input
	Document bool // PDF/document input
}

// ModelCapabilities resolves the input modalities the given model on the
// given provider supports. Unknown models (not in catalog) default to
// Vision=true but Audio=false, Video=false, and Document=false — see the
// struct comment for rationale.
func ModelCapabilitiesOf(provider *Provider, model string) ModelCapabilities {
	if provider == nil {
		return ModelCapabilities{Vision: true, Audio: false, Video: false, Document: false}
	}
	m := provider.FindModel(model)
	if m == nil {
		return ModelCapabilities{Vision: true, Audio: false, Video: false, Document: false}
	}
	return ModelCapabilities{Vision: m.Vision, Audio: m.Audio, Video: m.Video, Document: m.Document}
}

// ResolveMaxOutput picks the per-turn completion token ceiling. The model's
// advertised max output is used when known, but capped by the global settings
// default — the setting acts as a ceiling, not just a fallback. This prevents
// sending absurdly high max_tokens values (e.g. 1M for models that advertise
// it) which cause credit/balance rejections on gateways like OpenRouter.
func ResolveMaxOutput(provider *Provider, model string, settings Settings) int {
	cap := settings.MaxOutputTokens
	if cap <= 0 {
		cap = 65536
	}
	for _, m := range provider.Models {
		if m.ID == model && m.MaxOutput > 0 {
			if m.MaxOutput < cap {
				return m.MaxOutput
			}
			return cap
		}
	}
	return cap
}

// EffectiveContextWindow picks the window shown/used for a model: the
// model-advertised window wins (catalog value); the configured max_input_tokens
// is only a fallback for models that do not advertise one. Capping catalog
// models to the global setting confused users ("1M model, why 200k?").
func EffectiveContextWindow(modelWindow, maxInputTokens int) int {
	if modelWindow > 0 {
		return modelWindow
	}
	return maxInputTokens
}

// ResolveContextWindow picks the effective context window for compaction
// decisions: min(model context, max_input_tokens) when both are known, or the
// configured max_input_tokens fallback when the model does not advertise one.
func ResolveContextWindow(provider *Provider, model string, settings Settings) int {
	for _, m := range provider.Models {
		if m.ID == model && m.Context > 0 {
			return EffectiveContextWindow(m.Context, settings.MaxInputTokens)
		}
	}
	return settings.MaxInputTokens
}
