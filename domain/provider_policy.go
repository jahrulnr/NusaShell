package domain

// ModelCapabilities describes which input modalities the active chat model
// supports. Unknown models (not in catalog) default to all-true to preserve
// backward compatibility — providers will reject the attachment if
// unsupported, and the reactive error path handles that.
type ModelCapabilities struct {
	Vision bool // image input
	Audio  bool // audio input
	Video  bool // video input
}

// ModelCapabilities resolves the input modalities the given model on the
// given provider supports. Unknown models (not in catalog) default to all-
// true to preserve backward compatibility — providers will reject the
// attachment if unsupported, and the reactive error path handles that.
func ModelCapabilitiesOf(provider *Provider, model string) ModelCapabilities {
	if provider == nil {
		return ModelCapabilities{Vision: true, Audio: true, Video: true}
	}
	m := provider.FindModel(model)
	if m == nil {
		return ModelCapabilities{Vision: true, Audio: true, Video: true}
	}
	return ModelCapabilities{Vision: m.Vision, Audio: m.Audio, Video: m.Video}
}

// ModelSupportsVision reports whether the given model on the given provider
// supports image input. Returns true when the model metadata is unknown
// (not in catalog) to preserve backward compatibility — providers will
// reject the image if unsupported, and the reactive error path handles that.
func ModelSupportsVision(provider *Provider, model string) bool {
	return ModelCapabilitiesOf(provider, model).Vision
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
