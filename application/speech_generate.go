package application

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nusashell/domain"
)

const (
	ttsUnconfigured = "No speech generation model is configured. Ask the user to pick one in Settings → Speech generation, or install the offline piper model."

	// OfflineTTSProviderID is the pseudo provider id under which installed
	// piper voices appear in the Settings model picker. Selecting one sets
	// settings TTSProviderID to this value and routes generate_speech
	// straight to the local engine with the chosen voice.
	OfflineTTSProviderID = "piper"
)

// executeGenerateSpeech handles the generate_speech tool call.
// Route: explicit offline voice (provider "piper") first when the user
// picked an installed voice in Settings, then configured online TTS, then
// offline piper as fallback — mirroring the read_media ladder.
func (a *App) executeGenerateSpeech(run *TurnRun, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
	var args struct {
		Text   string  `json:"text"`
		Prompt string  `json:"prompt"`
		Voice  string  `json:"voice"`
		Format string  `json:"format"`
		Speed  float64 `json:"speed"`
	}
	if err := json.Unmarshal([]byte(toolCall.Args), &args); err != nil {
		return "error: invalid arguments", nil, fmt.Errorf("invalid args: %w", err)
	}
	text := strings.TrimSpace(args.Text)
	if text == "" {
		text = strings.TrimSpace(args.Prompt)
	}
	if text == "" {
		return "error: text is required", nil, fmt.Errorf("text is required")
	}
	if len(text) > 20000 {
		return "error: text too long", nil, fmt.Errorf("text exceeds 20000 characters")
	}
	var format string
	switch strings.ToLower(strings.TrimSpace(args.Format)) {
	case "", "mp3":
		format = "mp3"
	case "wav":
		format = "wav"
	case "opus":
		format = "opus"
	default:
		return "error: format must be one of: mp3, wav, opus", nil, errTTS("format must be one of: mp3, wav, opus")
	}

	req := TTSRequest{Text: text, Voice: strings.TrimSpace(args.Voice), Format: format, Speed: args.Speed}

	// 0. Explicit offline voice: the user selected an installed piper voice
	// in Settings (provider "piper"). The chosen voice wins over the tool
	// arg so Settings stays authoritative.
	if settings.TTSProviderID == OfflineTTSProviderID {
		req.Voice = settings.TTSModelID
		if out := a.synthesizeOfflineTTS(req); out != nil {
			return a.persistTTSText(run, toolCall.ID, out)
		}
		msg := "The selected offline piper voice is not available (was it uninstalled?). Reinstall it in Settings → Speech generation → Install offline text-to-speech."
		return msg, nil, fmt.Errorf("%s", msg)
	}

	// 1. Online route (explicit configuration wins).
	if settings.TTSProviderID != "" && settings.TTSModelID != "" {
		provider, apiKey, ok := a.resolveFallbackProvider(settings.TTSProviderID)
		if !ok {
			msg := fmt.Sprintf("Speech generation provider %q was not found or is disabled.", a.providerNameByID(settings.TTSProviderID))
			return msg, nil, fmt.Errorf("%s", msg)
		}
		if a.SpeechSynthesizerFactory != nil {
			synth, serr := a.SpeechSynthesizerFactory(provider, apiKey)
			if serr == nil {
				req.Model = settings.TTSModelID
				result, gerr := synth.Synthesize(run.Ctx, req)
				if gerr == nil {
					return a.persistTTSText(run, toolCall.ID, result)
				}
				a.log("warn", "tts", "online TTS failed (%v); trying offline piper", gerr)
			} else {
				a.log("warn", "tts", "online TTS factory failed: %v; trying offline piper", serr)
			}
		}
	}

	// 2. Offline piper fills the gap.
	if out := a.synthesizeOfflineTTS(req); out != nil {
		return a.persistTTSText(run, toolCall.ID, out)
	}

	return ttsUnconfigured, nil, fmt.Errorf("no TTS backend available")
}

func (a *App) persistTTSText(run *TurnRun, toolCallID string, result *TTSResult) (string, []domain.Attachment, error) {
	if result == nil || len(result.Audio) == 0 {
		return failGenerateSpeech("speech synthesizer returned no audio")
	}
	att, path, err := a.saveGeneratedMedia(run.ConversationID,
		fmt.Sprintf("speech_%s", time.Now().Format("20060102_150405")), "audio", result.Audio, true)
	if err != nil {
		return failGenerateSpeech(err.Error())
	}
	meta := map[string]any{
		"status": "completed", "provider": result.Provider, "model": result.Model,
		"voice": result.Voice, "media_type": result.MediaType, "file_path": path,
	}
	body := fmt.Sprintf("Speech generated and saved to %s.", path)
	return yamlMDApp(meta, body), []domain.Attachment{att}, nil
}

// synthesizeOfflineTTS runs the local piper engine when wired and available.
// Returns nil when the offline route cannot serve (caller falls through).
func (a *App) synthesizeOfflineTTS(req TTSRequest) *TTSResult {
	if a.OfflineSynthesizer == nil || !a.OfflineSynthesizer.Available() {
		return nil
	}
	res, err := a.OfflineSynthesizer.Synthesize(req)
	if err != nil {
		a.log("warn", "tts", "offline piper failed: %v", err)
		return nil
	}
	return res
}

func failGenerateSpeech(msg string) (string, []domain.Attachment, error) {
	return "error: " + msg, nil, fmt.Errorf("%s", msg)
}
