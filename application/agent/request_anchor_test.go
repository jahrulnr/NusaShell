package agent

import (
	"testing"

	"nusashell/domain"
)

// TestBuildTurnRequestReplacesUnreplayableCompactionBlobWithUserNudge pins the
// request half of the compaction anchor rule: a natively compacted epoch may
// hold only a post-checkpoint suffix, so when the room continues on a provider
// kind that cannot replay the opaque blob, the checkpoint is replaced by a
// synthetic user nudge. Responses/Codex keep replaying the checkpoint, and a
// real user message is never duplicated.
func TestBuildTurnRequestReplacesUnreplayableCompactionBlobWithUserNudge(t *testing.T) {
	svc := New(Deps{})
	blob := `[{"type":"compaction","encrypted_content":"OPAQUE"}]`
	conversation := &domain.Conversation{
		ID: "conv_blob",
		Messages: []domain.Message{
			{ID: "a1", Role: domain.RoleAssistant, Content: "post-checkpoint answer", Status: domain.StatusDone},
		},
		CompactionBlob:           blob,
		CompactionPrefixMessages: 1,
	}
	settings := domain.DefaultSettings()

	chatAdapter := ProviderContext{Kind: domain.ProviderChat}
	req := svc.buildTurnRequest(nil, chatAdapter, conversation, "a2", "deepseek-chat", "auto", nil, settings, false, nil, 1024, nil, ModelCapabilities{})
	if len(req.Messages) < 2 || req.Messages[0].Role != "user" || req.Messages[0].Content != userNudgeText {
		t.Fatalf("messages = %+v, want a leading synthetic user nudge", req.Messages)
	}
	if req.Messages[1].Role != "assistant" || req.Messages[1].Content != "post-checkpoint answer" {
		t.Fatalf("messages[1] = %+v, want the retained suffix after the nudge", req.Messages[1])
	}

	responsesAdapter := ProviderContext{Kind: domain.ProviderResponses}
	req = svc.buildTurnRequest(nil, responsesAdapter, conversation, "a2", "gpt-5.2", "auto", nil, settings, false, nil, 1024, nil, ModelCapabilities{})
	for _, m := range req.Messages {
		if m.Role == "user" {
			t.Fatalf("messages = %+v, want the checkpoint to anchor the epoch without a nudge", req.Messages)
		}
	}
	if req.CompactionBlob != blob {
		t.Fatalf("CompactionBlob = %q, want the checkpoint preserved for Responses", req.CompactionBlob)
	}

	withUser := *conversation
	withUser.Messages = append([]domain.Message{{ID: "u1", Role: domain.RoleUser, Content: "hello", Status: domain.StatusDone}}, conversation.Messages...)
	req = svc.buildTurnRequest(nil, chatAdapter, &withUser, "a2", "deepseek-chat", "auto", nil, settings, false, nil, 1024, nil, ModelCapabilities{})
	if len(req.Messages) < 2 || req.Messages[0].Role != "user" || req.Messages[0].Content != "hello" {
		t.Fatalf("messages = %+v, want the real user turn untouched", req.Messages)
	}

	noBlob := *conversation
	noBlob.CompactionBlob = ""
	req = svc.buildTurnRequest(nil, chatAdapter, &noBlob, "a2", "deepseek-chat", "auto", nil, settings, false, nil, 1024, nil, ModelCapabilities{})
	for _, m := range req.Messages {
		if m.Role == "user" {
			t.Fatalf("messages = %+v, want no nudge without a checkpoint", req.Messages)
		}
	}
}
