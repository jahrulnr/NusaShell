package media

import (
	"encoding/json"
	"fmt"
	"strings"

	"nusashell/application/service/ttserr"
	"nusashell/domain"
	clock "nusashell/pkg/time"
	"nusashell/pkg/yamlmd"
)

const (
	ttsUnconfigured = "No speech generation model is configured. Ask the user to pick one in Settings → Speech generation, or install the offline piper model."

	// OfflineTTSProviderID is the pseudo provider id under which installed
	// piper voices appear in the Settings model picker. Selecting one sets
	// settings TTSProviderID to this value and routes generate_speech
	// straight to the local engine with the chosen voice.
	OfflineTTSProviderID = "piper"
)

// ExecuteGenerateSpeech handles the generate_speech tool call.
// Route: explicit offline voice (provider "piper") first when the user
// picked an installed voice in Settings, then configured online TTS, then
// offline piper as fallback — mirroring the read_media ladder.
func (s *Service) ExecuteGenerateSpeech(call Call, toolCall domain.ToolCall, settings domain.Settings) (string, []domain.Attachment, error) {
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
		return "error: format must be one of: mp3, wav, opus", nil, ttserr.ErrTTS("format must be one of: mp3, wav, opus")
	}

	req := TTSRequest{Text: text, Voice: strings.TrimSpace(args.Voice), Format: format, Speed: args.Speed}

	if settings.TTSProviderID == OfflineTTSProviderID {
		req.Voice = settings.TTSModelID
		if out := s.synthesizeOfflineTTS(req); out != nil {
			return s.persistTTSText(call, toolCall.ID, out)
		}
		msg := "The selected offline piper voice is not available (was it uninstalled?). Reinstall it in Settings → Speech generation → Install offline text-to-speech."
		return msg, nil, fmt.Errorf("%s", msg)
	}

	if settings.TTSProviderID != "" && settings.TTSModelID != "" {
		provider, apiKey, ok := s.resolve(settings.TTSProviderID)
		if !ok {
			msg := fmt.Sprintf("Speech generation provider %q was not found or is disabled.", s.name(settings.TTSProviderID))
			return msg, nil, fmt.Errorf("%s", msg)
		}
		if s.speechSynth != nil {
			synth, serr := s.speechSynth(provider, apiKey)
			if serr == nil {
				req.Model = settings.TTSModelID
				result, gerr := synth.Synthesize(call.ctx(), req)
				if gerr == nil {
					return s.persistTTSText(call, toolCall.ID, result)
				}
				s.write("warn", "tts", "online TTS failed (%v); trying offline piper", gerr)
			} else {
				s.write("warn", "tts", "online TTS factory failed: %v; trying offline piper", serr)
			}
		}
	}

	if out := s.synthesizeOfflineTTS(req); out != nil {
		return s.persistTTSText(call, toolCall.ID, out)
	}

	return ttsUnconfigured, nil, fmt.Errorf("no TTS backend available")
}

func (s *Service) persistTTSText(call Call, _ string, result *TTSResult) (string, []domain.Attachment, error) {
	if result == nil || len(result.Audio) == 0 {
		return failGenerateSpeech("speech synthesizer returned no audio")
	}
	att, path, err := s.SaveGenerated(call.ConversationID,
		fmt.Sprintf("speech_%s", clock.NewTime().Format("20060102_150405")), "audio", result.Audio, true)
	if err != nil {
		return failGenerateSpeech(err.Error())
	}
	meta := map[string]any{
		"status": "completed", "provider": result.Provider, "model": result.Model,
		"voice": result.Voice, "media_type": result.MediaType, "file_path": path,
	}
	body := fmt.Sprintf("Speech generated and saved to %s.", path)
	return yamlmd.MD(meta, body), []domain.Attachment{att}, nil
}

func (s *Service) synthesizeOfflineTTS(req TTSRequest) *TTSResult {
	if s.offlineSynth == nil || !s.offlineSynth.Available() {
		return nil
	}
	res, err := s.offlineSynth.Synthesize(req)
	if err != nil {
		s.write("warn", "tts", "offline piper failed: %v", err)
		return nil
	}
	return res
}

func failGenerateSpeech(msg string) (string, []domain.Attachment, error) {
	return "error: " + msg, nil, fmt.Errorf("%s", msg)
}
