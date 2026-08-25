package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"nusashell/application"
	"nusashell/domain"
	aiutil "nusashell/infrastructure/ai/internal"
)

// DefaultBaseURL is the Google AI Studio Gemini API root.
const DefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// Adapter implements application.AIProvider for Google's Gemini
// generateContent API. Unlike OpenAI-compatible providers, Gemini uses a
// single multimodal endpoint for both chat and image generation.
type Adapter struct {
	BaseURL string
	APIKey  string
	Client  *http.Client
}

func (a *Adapter) Kind() domain.ProviderKind { return domain.ProviderGemini }

// generateContentURL builds the :generateContent endpoint for a model.
func (a *Adapter) generateContentURL(model string) string {
	base := strings.TrimRight(a.baseURL(), "/")
	return base + "/models/" + url.PathEscape(model) + ":generateContent"
}

// streamGenerateContentURL builds the :streamGenerateContent endpoint.
// alt=sse makes Gemini return Server-Sent Events.
func (a *Adapter) streamGenerateContentURL(model string) string {
	base := strings.TrimRight(a.baseURL(), "/")
	return base + "/models/" + url.PathEscape(model) + ":streamGenerateContent?alt=sse"
}

func (a *Adapter) baseURL() string {
	if a.BaseURL != "" {
		return a.BaseURL
	}
	return DefaultBaseURL
}

func (a *Adapter) headers() map[string]string {
	h := map[string]string{}
	if a.APIKey != "" {
		h["x-goog-api-key"] = a.APIKey
	}
	return h
}

// Complete sends a non-streaming generateContent request.
func (a *Adapter) Complete(ctx context.Context, req application.ChatRequest) (application.ChatResponse, error) {
	body := buildRequest(req, false)
	var resp generateContentResponse
	hdrs := a.headers()
	if err := aiutil.DoJSON(ctx, a.Client, http.MethodPost, a.generateContentURL(req.Model), hdrs, body, &resp); err != nil {
		return application.ChatResponse{}, err
	}
	if resp.Error != nil {
		return application.ChatResponse{}, &application.UpstreamError{
			Kind:       application.KindHTTPStatus,
			StatusCode: resp.Error.Code,
			Err:        fmt.Errorf("gemini: %s", resp.Error.Message),
		}
	}
	return toChatResponse(resp.Candidates, resp.UsageMetadata)
}

// Stream sends a streaming generateContent request via SSE.
func (a *Adapter) Stream(ctx context.Context, req application.ChatRequest, onDelta, onReasoning func(string)) (application.ChatResponse, error) {
	body := buildRequest(req, true)
	hdrs := a.headers()
	resp, err := aiutil.OpenSSE(ctx, a.Client, a.streamGenerateContentURL(req.Model), hdrs, body)
	if err != nil {
		if aiutil.ShouldRetryWithoutImages(err, req.Messages, ctx) {
			stripped := req
			stripped.Messages = aiutil.StripImages(req.Messages)
			return a.Stream(ctx, stripped, onDelta, onReasoning)
		}
		return application.ChatResponse{}, err
	}
	defer resp.Body.Close()

	var result application.ChatResponse
	toolByIndex := map[int]*domain.ToolCall{}
	completed := false
	var lastUsage *UsageMetadata

	streamErr := aiutil.ReadSSE(ctx, resp.Body, aiutil.DefaultIdleTimeout, func(ev aiutil.Event) error {
		if ev.Data == "[DONE]" {
			completed = true
			return nil
		}
		var chunk streamChunk
		if err := aiutil.DecodeData(ev, &chunk); err != nil {
			return err
		}
		if chunk.UsageMetadata != nil {
			lastUsage = chunk.UsageMetadata
		}
		for i, cand := range chunk.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" && !part.Thought {
					result.Content += part.Text
					if onDelta != nil {
						onDelta(part.Text)
					}
				}
				if part.Text != "" && part.Thought {
					result.Reasoning += part.Text
					if onReasoning != nil {
						onReasoning(part.Text)
					}
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					id := part.FunctionCall.ID
					if id == "" {
						id = part.FunctionCall.Name
					}
					tc := domain.ToolCall{
						ID:   id,
						Name: part.FunctionCall.Name,
						Args: string(args),
					}
					if part.FunctionCall.ThoughtSignature != "" {
						if tc.Opaque == nil {
							tc.Opaque = map[string]any{}
						}
						tc.Opaque["thought_signature"] = part.FunctionCall.ThoughtSignature
					}
					toolByIndex[i] = &tc
					result.ToolCalls = append(result.ToolCalls, tc)
				}
			}
			if cand.FinishReason == "STOP" {
				completed = true
				result.StopReason = "stop"
			} else if cand.FinishReason == "MAX_TOKENS" {
				completed = true
				result.StopReason = "length"
			} else if cand.FinishReason != "" {
				completed = true
				result.StopReason = strings.ToLower(cand.FinishReason)
			}
		}
		return nil
	})
	if streamErr != nil {
		return result, streamErr
	}
	_ = completed
	if lastUsage != nil {
		result.Usage = usageFromMetadata(*lastUsage)
	}
	return result, nil
}

func usageFromMetadata(u UsageMetadata) application.ChatUsage {
	return application.ChatUsage{
		InputTokens:  u.PromptTokenCount,
		OutputTokens: u.CandidatesTokenCount,
		CacheRead:    u.CachedContentTokenCount,
	}
}

func toChatResponse(cands []ResponseCandidate, u UsageMetadata) (application.ChatResponse, error) {
	if len(cands) == 0 {
		return application.ChatResponse{Usage: usageFromMetadata(u)}, nil
	}
	var resp application.ChatResponse
	resp.Usage = usageFromMetadata(u)
	cand := cands[0]
	for _, part := range cand.Content.Parts {
		if part.Text != "" && !part.Thought {
			resp.Content += part.Text
		}
		if part.Text != "" && part.Thought {
			resp.Reasoning += part.Text
		}
		if part.FunctionCall != nil {
			args, _ := json.Marshal(part.FunctionCall.Args)
			id := part.FunctionCall.ID
			if id == "" {
				id = part.FunctionCall.Name
			}
			tc := domain.ToolCall{
				ID:   id,
				Name: part.FunctionCall.Name,
				Args: string(args),
			}
			if part.FunctionCall.ThoughtSignature != "" {
				if tc.Opaque == nil {
					tc.Opaque = map[string]any{}
				}
				tc.Opaque["thought_signature"] = part.FunctionCall.ThoughtSignature
			}
			resp.ToolCalls = append(resp.ToolCalls, tc)
		}
	}
	resp.StopReason = strings.ToLower(cand.FinishReason)
	return resp, nil
}

// buildRequest converts application.ChatRequest to Gemini generateContentRequest.
func buildRequest(req application.ChatRequest, stream bool) *generateContentRequest {
	out := &generateContentRequest{
		Contents: buildContents(req.Messages),
	}
	if req.System != "" {
		out.SystemInstruction = &Content{
			Parts: []Part{{Text: req.System}},
		}
	}
	if len(req.Tools) > 0 {
		out.Tools = buildTools(req.Tools)
	}
	gc := &GenerationConfig{}
	if req.MaxTokens > 0 {
		gc.MaxOutputTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		gc.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		gc.TopP = *req.TopP
	}
	if req.TopK != nil {
		gc.TopK = *req.TopK
	}
	out.GenerationConfig = gc
	return out
}

func buildContents(msgs []application.ChatMessage) []Content {
	out := make([]Content, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user", "system":
			c := Content{Role: "user", Parts: []Part{{Text: m.Content}}}
			for _, att := range m.Attachments {
				if blob := attachmentToBlob(att); blob != nil {
					c.Parts = append(c.Parts, Part{InlineData: blob})
				}
			}
			out = append(out, c)
		case "assistant":
			c := Content{Role: "model"}
			if m.Content != "" {
				c.Parts = append(c.Parts, Part{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]interface{}
				_ = json.Unmarshal([]byte(tc.Args), &args)
				fc := &FunctionCall{
					ID:   tc.ID,
					Name: aiutil.SanitizeToolName(tc.Name),
					Args: args,
				}
				if sig, ok := tc.Opaque["thought_signature"].(string); ok && sig != "" {
					fc.ThoughtSignature = sig
				}
				c.Parts = append(c.Parts, Part{FunctionCall: fc})
			}
			out = append(out, c)
		case "tool":
			// Gemini has no "function" role. Function responses are sent as
			// role "user" with a functionResponse part. The model pairs the
			// response to the prior functionCall by name/id, not by role.
			c := Content{Role: "user"}
			if m.ToolResult != nil {
				resp := map[string]interface{}{"response": m.ToolResult.Content}
				c.Parts = []Part{{FunctionResponse: &FunctionResponse{
					ID:       m.ToolResult.ToolCallID,
					Name:     aiutil.SanitizeToolName(m.ToolResult.Name),
					Response: resp,
				}}}
			}
			out = append(out, c)
		}
	}
	return out
}

func buildTools(tools []application.ToolDef) []Tool {
	decls := make([]FunctionDeclaration, 0, len(tools))
	for _, t := range tools {
		decls = append(decls, FunctionDeclaration{
			Name:        aiutil.SanitizeToolName(t.Name),
			Description: t.Description,
			Parameters:  schemaFromMap(t.InputSchema),
		})
	}
	return []Tool{{FunctionDeclarations: decls}}
}

func schemaFromMap(m map[string]any) *Schema {
	if len(m) == 0 {
		return nil
	}
	s := &Schema{}
	if t, ok := m["type"].(string); ok {
		s.Type = t
	}
	if d, ok := m["description"].(string); ok {
		s.Description = d
	}
	if props, ok := m["properties"].(map[string]any); ok {
		s.Properties = make(map[string]*Schema, len(props))
		for k, v := range props {
			if vm, ok := v.(map[string]any); ok {
				s.Properties[k] = schemaFromMap(vm)
			}
		}
	}
	if req, ok := m["required"].([]any); ok {
		s.Required = make([]string, 0, len(req))
		for _, r := range req {
			if rs, ok := r.(string); ok {
				s.Required = append(s.Required, rs)
			}
		}
	}
	if items, ok := m["items"].(map[string]any); ok {
		s.Items = schemaFromMap(items)
	}
	if e, ok := m["enum"].([]any); ok {
		s.Enum = make([]string, 0, len(e))
		for _, ev := range e {
			if es, ok := ev.(string); ok {
				s.Enum = append(s.Enum, es)
			}
		}
	}
	return s
}

func attachmentToBlob(att domain.Attachment) *Blob {
	if att.DataURL == "" {
		return nil
	}
	// data:image/png;base64,iVBOR...
	idx := strings.Index(att.DataURL, ",")
	if idx < 0 {
		return nil
	}
	header := att.DataURL[:idx]
	data := att.DataURL[idx+1:]
	mime := ""
	if strings.HasPrefix(header, "data:") {
		mime = strings.TrimPrefix(header, "data:")
		mime = strings.Split(mime, ";")[0]
	}
	if mime == "" || data == "" {
		return nil
	}
	return &Blob{MimeType: mime, Data: data}
}
