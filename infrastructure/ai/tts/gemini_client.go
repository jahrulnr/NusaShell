package tts

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/pkg/httpclient"
)

// GeminiClient synthesizes speech through Google's generateContent surface
// with responseModalities ["AUDIO"] + speechConfig (single-speaker
// prebuiltVoiceConfig). It is the Gemini-kind TTS backend, mirroring the
// imagegen.BackendGemini and videogen.GeminiClient routing decisions: TTS
// model ids come from the shared GET /v1beta/models catalog + classification,
// not a dedicated media lister.
//
// Format: Gemini TTS returns raw PCM (audio/L16;rate=24000, 16-bit, mono) in
// candidates[].content.parts[].inlineData. The PCM is wrapped into a WAV
// container so the persisted artifact is playable. The tool's mp3/opus
// formats are not produced by this surface — the artifact is always WAV and
// the requested format is recorded as a deviation (mirroring how piper always
// returns WAV). Speed has no Gemini TTS equivalent and is ignored.
//
// Multi-speaker (speechConfig with a voice array, up to 2 speakers) is out of
// scope for this run; only single-speaker prebuiltVoiceConfig is wired.
type GeminiClient struct {
	BaseURL string // normalized to end with /v1beta
	APIKey  string
	HTTP    *http.Client
}

// geminiTTSRequest is the generateContent body for TTS.
type geminiTTSRequest struct {
	Contents         []geminiTTSContent         `json:"contents"`
	GenerationConfig *geminiTTSGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiTTSContent struct {
	Role  string          `json:"role,omitempty"`
	Parts []geminiTTSPart `json:"parts"`
}

type geminiTTSPart struct {
	Text string `json:"text,omitempty"`
}

type geminiTTSGenerationConfig struct {
	ResponseModalities []string               `json:"responseModalities,omitempty"`
	SpeechConfig       *geminiTTSSpeechConfig `json:"speechConfig,omitempty"`
}

type geminiTTSSpeechConfig struct {
	VoiceConfig *geminiTTSVoiceConfig `json:"voiceConfig,omitempty"`
}

type geminiTTSVoiceConfig struct {
	PrebuiltVoiceConfig *geminiTTSPrebuiltVoice `json:"prebuiltVoiceConfig,omitempty"`
}

type geminiTTSPrebuiltVoice struct {
	VoiceName string `json:"voiceName,omitempty"`
}

type geminiTTSResponse struct {
	Candidates []geminiTTSCandidate `json:"candidates"`
	Error      *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error,omitempty"`
}

type geminiTTSCandidate struct {
	Content *geminiTTSResponseContent `json:"content,omitempty"`
}

type geminiTTSResponseContent struct {
	Parts []geminiTTSResponsePart `json:"parts,omitempty"`
}

type geminiTTSResponsePart struct {
	Text       string           `json:"text,omitempty"`
	InlineData *geminiTTSInline `json:"inlineData,omitempty"`
}

type geminiTTSInline struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"` // base64-encoded audio
}

// DefaultGeminiTTSVoice is the fallback prebuilt voice when TTSRequest.Voice
// is empty. Kore is the documented Hermes/runtime default.
const DefaultGeminiTTSVoice = "Kore"

func (c *GeminiClient) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return httpclient.NewWithTimeout(httpclient.DefaultRequestTimeout)
}

// Synthesize posts a generateContent request with responseModalities ["AUDIO"]
// + speechConfig single-speaker voiceName, decodes the inlineData audio part,
// wraps raw PCM into a WAV container, and returns a TTSResult. Speed is
// ignored (Gemini TTS has no speed control). The requested mp3/opus format is
// not produced by this surface; the artifact is always WAV.
func (c *GeminiClient) Synthesize(ctx context.Context, req application.TTSRequest) (*application.TTSResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("tts: model is required")
	}
	if strings.TrimSpace(req.Text) == "" {
		return nil, fmt.Errorf("tts: text is required")
	}
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		voice = DefaultGeminiTTSVoice
	}

	body := geminiTTSRequest{
		Contents: []geminiTTSContent{{
			Role:  "user",
			Parts: []geminiTTSPart{{Text: req.Text}},
		}},
		GenerationConfig: &geminiTTSGenerationConfig{
			ResponseModalities: []string{"AUDIO"},
			SpeechConfig: &geminiTTSSpeechConfig{
				VoiceConfig: &geminiTTSVoiceConfig{
					PrebuiltVoiceConfig: &geminiTTSPrebuiltVoice{VoiceName: voice},
				},
			},
		},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	model := normalizeGeminiTTSModelID(req.Model)
	url := strings.TrimRight(c.BaseURL, "/") + "/models/" + model + ":generateContent"

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("x-goog-api-key", c.APIKey)

	resp, err := c.httpClient().Do(httpReq)
	if err != nil {
		return nil, &domain.ProviderError{Kind: domain.KindConnect, Temporary: true, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, mapGeminiTTSError(resp.StatusCode, raw)
	}
	var decoded geminiTTSResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("gemini tts: decode response: %w", err)
	}
	if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
		return nil, &domain.ProviderError{
			StatusCode: decoded.Error.Code,
			Err:        fmt.Errorf("%s", strings.TrimSpace(decoded.Error.Message)),
		}
	}

	// Find the first inlineData audio part across all candidates.
	var inline *geminiTTSInline
	for _, cand := range decoded.Candidates {
		if cand.Content == nil {
			continue
		}
		for i := range cand.Content.Parts {
			if cand.Content.Parts[i].InlineData != nil && cand.Content.Parts[i].InlineData.Data != "" {
				inline = cand.Content.Parts[i].InlineData
				break
			}
		}
		if inline != nil {
			break
		}
	}
	if inline == nil {
		return nil, fmt.Errorf("gemini tts: generateContent returned no audio data (using a text model with AUDIO modality returns 400)")
	}
	audio, err := base64.StdEncoding.DecodeString(inline.Data)
	if err != nil {
		return nil, fmt.Errorf("gemini tts: decode inline audio: %w", err)
	}
	if len(audio) == 0 {
		return nil, fmt.Errorf("gemini tts: empty audio response")
	}

	// Gemini TTS returns raw PCM (audio/L16;rate=24000) or occasionally WAV.
	// Wrap PCM into a WAV container so the artifact is playable. The surface
	// cannot produce mp3/opus — the artifact is always WAV regardless of the
	// requested format (documented deviation; never label PCM as mp3).
	audio, mediaType := normalizeGeminiTTSAudio(inline.MimeType, audio)
	return &application.TTSResult{
		Audio:     audio,
		MediaType: mediaType,
		Ext:       "wav",
		Provider:  "gemini",
		Model:     req.Model,
		Voice:     voice,
	}, nil
}

// normalizeGeminiTTSModelID strips "models/" and "<provider>/" prefixes so
// the generateContent path carries only the bare model id the API expects.
func normalizeGeminiTTSModelID(model string) string {
	name := strings.TrimSpace(model)
	segments := strings.Split(name, "/")
	if len(segments) > 1 {
		name = segments[len(segments)-1]
	}
	return name
}

// normalizeGeminiTTSAudio wraps raw PCM into a WAV container when the
// inlineData MIME is PCM-family (audio/L16;rate=...). Already-containerized
// WAV (audio/wav) is passed through. Returns the audio bytes and the
// effective media type.
func normalizeGeminiTTSAudio(mimeType string, audio []byte) ([]byte, string) {
	mime := strings.TrimSpace(strings.ToLower(mimeType))
	if mime == "" || strings.HasPrefix(mime, "audio/wav") || strings.HasPrefix(mime, "audio/wave") || strings.HasPrefix(mime, "audio/x-wav") {
		return audio, "audio/wav"
	}
	if strings.HasPrefix(mime, "audio/l16") || strings.HasPrefix(mime, "audio/pcm") {
		sampleRate, bits, channels := parsePCMMimeType(mime)
		if bits == 0 {
			bits = 16
		}
		if channels == 0 {
			channels = 1
		}
		if sampleRate == 0 {
			sampleRate = 24000
		}
		return pcmToWAV(audio, sampleRate, bits, channels), "audio/wav"
	}
	// Unknown MIME — return as-is with the declared type; the caller will
	// still persist WAV metadata since this surface only produces PCM/WAV.
	return audio, "audio/wav"
}

// parsePCMMimeType extracts sample rate, bits per sample, and channels from
// an audio/L16;rate=24000;bit=16;channels=1 style MIME parameter string.
// Missing parameters return 0 so the caller can apply defaults.
func parsePCMMimeType(mime string) (sampleRate, bits, channels int) {
	for _, part := range strings.Split(mime, ";") {
		part = strings.TrimSpace(part)
		if eq := strings.Index(part, "="); eq > 0 {
			key := strings.ToLower(strings.TrimSpace(part[:eq]))
			val := strings.TrimSpace(part[eq+1:])
			n, _ := strconv.Atoi(val)
			switch key {
			case "rate":
				sampleRate = n
			case "bit", "bits":
				bits = n
			case "channels":
				channels = n
			}
		}
	}
	return
}

// pcmToWAV wraps raw PCM samples in a minimal 44-byte WAV header. Supports
// 8/16/24/32-bit PCM, mono or multi-channel. This is the smallest container
// that makes the artifact playable without adding an encoder dependency.
func pcmToWAV(pcm []byte, sampleRate, bitsPerSample, channels int) []byte {
	if sampleRate <= 0 {
		sampleRate = 24000
	}
	if bitsPerSample <= 0 {
		bitsPerSample = 16
	}
	if channels <= 0 {
		channels = 1
	}
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := len(pcm)
	header := make([]byte, 44)
	copy(header[0:4], []byte("RIFF"))
	binary.LittleEndian.PutUint32(header[4:8], uint32(36+dataSize))
	copy(header[8:12], []byte("WAVE"))
	copy(header[12:16], []byte("fmt "))
	binary.LittleEndian.PutUint32(header[16:20], 16) // PCM fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(header[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(header[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(header[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(header[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(header[34:36], uint16(bitsPerSample))
	copy(header[36:40], []byte("data"))
	binary.LittleEndian.PutUint32(header[40:44], uint32(dataSize))
	return append(header, pcm...)
}

// mapGeminiTTSError converts a non-2xx Gemini TTS response into a
// domain.ProviderError. 4xx are hard failures (validation/auth/billing);
// 5xx stay retriable for the caller's shared policy. TTS may be billable, so
// 429 is surfaced as a hard failure (RetryAfter=0) instead of a blind retry
// loop. Provider messages (e.g. "using a text model with AUDIO modality") are
// surfaced verbatim. No credential value is ever copied into the error.
func mapGeminiTTSError(status int, raw []byte) error {
	var parsed struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &parsed)
	detail := strings.TrimSpace(parsed.Error.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(raw))
		if len(detail) > 800 {
			detail = detail[:800]
		}
	}
	if status >= 500 {
		return &domain.ProviderError{
			Kind:       domain.KindHTTPStatus,
			StatusCode: status,
			Temporary:  true,
			Err:        fmt.Errorf("gemini tts failed (HTTP %d): %s", status, detail),
		}
	}
	return &domain.ProviderError{
		StatusCode: status,
		Err:        fmt.Errorf("gemini tts failed (HTTP %d): %s", status, detail),
	}
}
