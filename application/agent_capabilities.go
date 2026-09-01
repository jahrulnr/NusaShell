package application

import (
	"fmt"
	"sort"
	"strings"

	"nusashell/application/service/learnedparams"
	"nusashell/application/service/modeloverrides"
	"nusashell/application/service/tooloutput"
	"nusashell/domain"
)

type ModelCapabilities struct {
	Vision   bool // image input
	Audio    bool // audio input
	Video    bool // video input
	Document bool // PDF/document input
	// Reasoning reports whether the model supports reasoning/thinking mode.
	// When false, effort levels other than "auto"/"none" are stripped
	// before the request is sent so non-reasoning models do not receive a
	// thinking field they would reject or ignore.
	Reasoning bool
	// ReasoningReplay is true when the upstream requires reasoning_content
	// (Chat Completions) or reasoning items (Responses API) to be echoed
	// back on every assistant message in subsequent turns. Resolved from
	// the model's InterleavedField catalog signal or a provider/model
	// pattern fallback.
	ReasoningReplay bool
}

// modelCapabilitiesWithLearned is the testable form of model capabilities
// resolution: it accepts a learnedParamsCache for applying learned disabled
// modalities and a modelOverridesCache for applying manual overrides. When
// cache is nil, no learned overrides are applied; when manual is nil, no
// manual overrides are applied. Precedence: catalog → learned → manual
// (manual is applied last so it always wins).
func modelCapabilitiesWithLearned(provider *domain.Provider, model string, cache *learnedparams.Cache, manual *modeloverrides.Cache) ModelCapabilities {
	dc := domain.ModelCapabilitiesOf(provider, model)
	caps := ModelCapabilities{Vision: dc.Vision, Audio: dc.Audio, Video: dc.Video, Document: dc.Document}
	if provider != nil {
		if m := provider.FindModel(model); m != nil {
			caps.Reasoning = m.Reasoning
			caps.ReasoningReplay = domain.RequiresReasoningReplay(provider.ID, model, m.InterleavedField)
		} else {
			caps.ReasoningReplay = domain.RequiresReasoningReplay(provider.ID, model, "")
		}
	}
	// Apply learned disabled modalities as proactive override. A previous
	// 400 that taught us "this model is text-only" (or lacks audio/video)
	// should prevent sending unsupported content on the first request,
	// not just on retry.
	providerID := ""
	if provider != nil {
		providerID = provider.ID
	}
	for _, modality := range cache.DisabledModalities(providerID, model) {
		switch strings.ToLower(modality) {
		case "vision":
			caps.Vision = false
		case "audio":
			caps.Audio = false
		case "video":
			caps.Video = false
		case "document":
			caps.Document = false
		}
	}
	// Manual overrides win over both the catalog and learned adaptations.
	// Applied last so an operator/review-agent correction is never clobbered
	// by a learned rule re-derived from the (already mutated) provider.
	if manual != nil && provider != nil {
		if o := manual.Get(provider.ID, model); o != nil {
			if o.Vision != nil {
				caps.Vision = *o.Vision
			}
			if o.Audio != nil {
				caps.Audio = *o.Audio
			}
			if o.Video != nil {
				caps.Video = *o.Video
			}
			if o.Document != nil {
				caps.Document = *o.Document
			}
			if o.Reasoning != nil {
				caps.Reasoning = *o.Reasoning
			}
		}
	}
	return caps
}

func chatMessages(c *domain.Conversation, pendingMsgID string, caps ModelCapabilities) []ChatMessage {
	src := c.Messages
	if domain.HydrationPrecedesFirstUser(src) {
		src = domain.RelocateHydrationAfterFirstUser(src)
	}
	var out []ChatMessage
	for _, m := range src {
		switch m.Role {
		case domain.RoleUser:
			content := m.Content
			attachments := m.Attachments
			if !caps.Vision && domain.HasAttachmentOfType(attachments, "image") {
				imageAtts := domain.FilterAttachmentsByType(attachments, "image")
				attachments = domain.StripAttachmentsByType(attachments, "image")
				placeholder := domain.OmittedPlaceholderFor("image", "read_media", imageAtts)
				if content == "" {
					content = placeholder
				} else if !domain.ContainsOmissionNote(content, "image") {
					content = content + "\n\n" + placeholder
				}
			}
			// Vision model: keep the image pixels visible AND surface the
			// absolute file path so the model can reference it for image-to-
			// image editing via generate_image. Unlike the non-vision branch
			// above, the attachment is not stripped — this only appends a path
			// note with distinct wording so the model does not mistake it for
			// a missing/omitted image.
			if caps.Vision && domain.HasAttachmentOfType(attachments, "image") {
				imageAtts := domain.FilterAttachmentsByType(attachments, "image")
				if note := domain.VisionImagePathNote(imageAtts); note != "" && !domain.ContainsVisionImageNote(content) {
					if content == "" {
						content = note
					} else {
						content = content + "\n\n" + note
					}
				}
			}
			if !caps.Audio && domain.HasAttachmentOfType(attachments, "audio") {
				audioAtts := domain.FilterAttachmentsByType(attachments, "audio")
				attachments = domain.StripAttachmentsByType(attachments, "audio")
				placeholder := domain.OmittedPlaceholderFor("audio", "read_media", audioAtts)
				if content == "" {
					content = placeholder
				} else if !domain.ContainsOmissionNote(content, "audio") {
					content = content + "\n\n" + placeholder
				}
			}
			if !caps.Video && domain.HasAttachmentOfType(attachments, "video") {
				videoAtts := domain.FilterAttachmentsByType(attachments, "video")
				attachments = domain.StripAttachmentsByType(attachments, "video")
				placeholder := domain.OmittedPlaceholderFor("video", "read_media", videoAtts)
				if content == "" {
					content = placeholder
				} else if !domain.ContainsOmissionNote(content, "video") {
					content = content + "\n\n" + placeholder
				}
			}
			if !caps.Document && domain.HasAttachmentOfType(attachments, "file") {
				fileAtts := domain.FilterAttachmentsByType(attachments, "file")
				attachments = domain.StripAttachmentsByType(attachments, "file")
				placeholder := domain.OmittedPlaceholderFor("document", "read_media", fileAtts)
				if content == "" {
					content = placeholder
				} else if !domain.ContainsOmissionNote(content, "document") {
					content = content + "\n\n" + placeholder
				}
			}
			// Folder attachments are path-only references. Inject the path
			// as text so the agent can use file tools to explore the
			// directory. The attachment itself is stripped from the
			// attachment list (it has no bytes for the provider).
			if domain.HasAttachmentOfType(attachments, "folder") {
				folderAtts := domain.FilterAttachmentsByType(attachments, "folder")
				attachments = domain.StripAttachmentsByType(attachments, "folder")
				placeholder := domain.FolderPlaceholderFor(folderAtts)
				if content == "" {
					content = placeholder
				} else {
					content = content + "\n\n" + placeholder
				}
			}
			out = append(out, ChatMessage{Role: "user", Content: content, Attachments: attachments})
		case domain.RoleAssistant:
			content := visibleText(m.Content)
			if m.ID == pendingMsgID && content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			if content == "" && m.Reasoning == "" && len(m.ToolCalls) == 0 {
				continue
			}
			cm := ChatMessage{Role: "assistant", Content: content, Reasoning: m.Reasoning, ToolCalls: m.ToolCalls}
			out = append(out, cm)
			for _, tc := range m.ToolCalls {
				// Summarize first (show/subagent get short summaries),
				// then filter attachments by model capability (appends
				// notes for unsupported media), then wrap the combined
				// content in the untrusted envelope. This order ensures
				// capability-filter notes land INSIDE the envelope —
				// appending notes after wrapping would let a malicious
				// tool craft a file path containing injection
				// instructions that the model treats as trusted content.
				toolContent := tooloutput.SummarizeToolContent(tc.Name, tc.Output)
				toolAtts := tc.OutputAttachments
				if len(toolAtts) > 0 {
					toolAtts, toolContent = filterToolAttachmentsByCaps(toolAtts, toolContent, caps)
				}
				toolContent = tooloutput.WrapToolOutput(tc.Name, toolContent)
				out = append(out, ChatMessage{Role: "tool", ToolResult: &ToolResult{
					ToolCallID: tc.ID, Name: tc.Name, Content: toolContent,
					Attachments: toolAtts,
				}})
			}
		case domain.RoleSystem:
			// folded into the system prompt by buildSystemPrompt
		}
	}
	return out
}

// filterToolAttachmentsByCaps strips media attachments from a tool result
// when the active model does not support the corresponding modality, and
// appends a text note to the content so the model knows the media exists
// but couldn't be delivered. This prevents provider errors when a read_*
// tool returns media that the model can't process:
//   - audio sent to a non-audio model via input_audio/image_url transport
//     (Nvidia NIM rejects with "Failed to load image from data:audio/...")
//   - video sent to a non-video model via video_url/input_image transport
//     (Stealth rejects with HTTP 400; OpenAI doesn't support video
//     natively at all — only frames as input_image)
//   - image sent to a non-vision model
//
// The same gating applies to user-authored attachments in chatMessages
// (stripped + placeholder) and to proactive fallback enrichment
// (enrichWithAudioDescriptions / enrichWithVideoDescriptions describe the
// media via a fallback model so the text-only model still gets the content).
func filterToolAttachmentsByCaps(atts []domain.Attachment, content string, caps ModelCapabilities) ([]domain.Attachment, string) {
	if len(atts) == 0 {
		return atts, content
	}
	filtered := make([]domain.Attachment, 0, len(atts))
	var notes []string
	for _, att := range atts {
		switch att.Type {
		case "image":
			if !caps.Vision {
				notes = append(notes, fmt.Sprintf("[Image %q was loaded but cannot be shown to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "audio":
			if !caps.Audio {
				notes = append(notes, fmt.Sprintf("[Audio %q was loaded but cannot be played to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "video":
			if !caps.Video {
				notes = append(notes, fmt.Sprintf("[Video %q was loaded but cannot be shown to this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		case "file":
			if !caps.Document {
				notes = append(notes, fmt.Sprintf("[Document %q was loaded but cannot be read by this model. File path: %s]", att.Name, att.FilePath))
				continue
			}
		}
		filtered = append(filtered, att)
	}
	if len(notes) > 0 {
		if content != "" {
			content += "\n"
		}
		content += strings.Join(notes, "\n")
	}
	return filtered, content
}

// saveAttachmentsToDisk writes image/file attachments to the attachment store
// and fills in the FilePath field with the absolute path. Failures are
// logged but non-fatal — the attachment still has its DataURL for inline
// use by vision-capable models.
func (a *App) saveAttachmentsToDisk(conversationID string, attachments []domain.Attachment) {
	if a.Attachments == nil {
		return
	}
	for i := range attachments {
		att := &attachments[i]
		if att.Type == "text" || att.Type == "folder" || att.FilePath != "" {
			continue
		}
		path, err := a.Attachments.Save(conversationID, *att)
		if err != nil {
			a.log("warn", "attachments", "failed to save %s to disk: %v", att.Name, err)
			continue
		}
		att.FilePath = path
	}
}

// toolRoundSignature returns a deterministic, order-independent signature for
// a set of tool calls. Tools are sorted by name+args so the same set in any
// order produces the same signature. This lets the repeated-tool guard detect
// parallel-tool loops (e.g. GPT-5.6 Luna calling 6 mcp_enable in a different
// order each round).
func toolRoundSignature(calls []domain.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	sigs := make([]string, len(calls))
	for i, c := range calls {
		sigs[i] = c.Name + "|" + c.Args
	}
	sort.Strings(sigs)
	return strings.Join(sigs, "\n")
}

// repeatedToolGuard detects when the agent calls the same set of tools with
// the same arguments for N consecutive rounds without producing text. When
// the streak reaches the limit, check returns true and the caller strips
// tools for the next round to break the loop. A round with text content or a
// different tool signature resets the streak. limit=0 disables the guard.
type repeatedToolGuard struct {
	limit   int
	streak  int
	lastSig string
}

// check updates the guard state and returns true if the repeated-tool limit
// has been reached (the caller should force a text-only round). After firing,
// the guard resets so a new streak must build up before firing again.
func (g *repeatedToolGuard) check(calls []domain.ToolCall, content string) bool {
	if g.limit <= 0 || len(calls) == 0 || content != "" {
		g.streak = 0
		g.lastSig = ""
		return false
	}
	sig := toolRoundSignature(calls)
	if sig == g.lastSig {
		g.streak++
	} else {
		g.streak = 1
		g.lastSig = sig
	}
	if g.streak >= g.limit {
		g.streak = 0
		g.lastSig = ""
		return true
	}
	return false
}
