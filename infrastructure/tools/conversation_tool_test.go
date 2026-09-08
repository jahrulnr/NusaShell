package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"nusashell/application"
	"nusashell/application/conversation"
)

type stubConversationMessenger struct {
	listFn           func(currID string, limit, offset int) (int, []application.ConversationSummaryDTO, error)
	searchFn         func(currID, query string, limit, offset int) (int, []application.ConversationSummaryDTO, error)
	sendFn           func(currID, targetID, content string) error
	infoFn           func(id string, chunk *int) (application.ConversationInfoDTO, error)
	readFn           func(id string, chunk *int, start, end *int) (application.ConversationReadResult, error)
	searchMessagesFn func(id, query string, limit, offset int) (int, []application.ConversationMessageHitDTO, error)
}

func (s *stubConversationMessenger) List(currID string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
	if s.listFn != nil {
		return s.listFn(currID, limit, offset)
	}
	return 0, nil, nil
}

func (s *stubConversationMessenger) Search(currID, query string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
	if s.searchFn != nil {
		return s.searchFn(currID, query, limit, offset)
	}
	return 0, nil, nil
}

func (s *stubConversationMessenger) Send(currID, targetID, content string) error {
	if s.sendFn != nil {
		return s.sendFn(currID, targetID, content)
	}
	return nil
}

func (s *stubConversationMessenger) Info(id string, chunk *int) (application.ConversationInfoDTO, error) {
	if s.infoFn != nil {
		return s.infoFn(id, chunk)
	}
	return application.ConversationInfoDTO{}, nil
}

func (s *stubConversationMessenger) Read(id string, chunk *int, start, end *int) (application.ConversationReadResult, error) {
	if s.readFn != nil {
		return s.readFn(id, chunk, start, end)
	}
	return application.ConversationReadResult{}, nil
}

func (s *stubConversationMessenger) SearchMessages(id, query string, limit, offset int) (int, []application.ConversationMessageHitDTO, error) {
	if s.searchMessagesFn != nil {
		return s.searchMessagesFn(id, query, limit, offset)
	}
	return 0, nil, nil
}

func TestToolboxConversationOps(t *testing.T) {
	messenger := &stubConversationMessenger{
		listFn: func(currID string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
			return 1, []application.ConversationSummaryDTO{
				{ID: "conv_target", Title: "Target Room", Summary: "Working on feature X", Status: "idle", UpdatedAt: time.Now().Format(time.RFC3339)},
			}, nil
		},
		searchFn: func(currID, query string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
			return 1, []application.ConversationSummaryDTO{
				{ID: "conv_target", Title: "Target Room", Summary: "Working on feature X", Status: "idle", UpdatedAt: time.Now().Format(time.RFC3339), Match: "title"},
			}, nil
		},
		sendFn: func(currID, targetID, content string) error {
			if currID != "conv_source" {
				t.Fatalf("currID = %q, want conv_source", currID)
			}
			if targetID != "conv_target" {
				t.Fatalf("targetID = %q, want conv_target", targetID)
			}
			if content != "Hello agent" {
				t.Fatalf("content = %q, want 'Hello agent'", content)
			}
			return nil
		},
		infoFn: func(id string, chunk *int) (application.ConversationInfoDTO, error) {
			if id != "conv_target" || chunk != nil {
				t.Fatalf("info args id=%q chunk=%v", id, chunk)
			}
			return application.ConversationInfoDTO{ID: id, Title: "Target Room", TurnCount: 3, MessageCount: 6, ChunkCount: 1}, nil
		},
		readFn: func(id string, chunk *int, start, end *int) (application.ConversationReadResult, error) {
			s, e := 0, 0
			if start != nil {
				s = *start
			}
			if end != nil {
				e = *end
			}
			return application.ConversationReadResult{
				ID: id, Start: s, End: e, TurnCount: 1,
				Messages: []conversation.ReadMsgDTO{
					{Turn: 0, ID: "u1", Role: "user", Content: "hello"},
				},
			}, nil
		},
		searchMessagesFn: func(id, query string, limit, offset int) (int, []application.ConversationMessageHitDTO, error) {
			if id != "conv_target" || query != "hello" {
				t.Fatalf("searchMessages id=%q query=%q", id, query)
			}
			return 1, []application.ConversationMessageHitDTO{
				{ConversationID: id, Turn: 0, MessageID: "u1", Role: "user", Snippet: "hello"},
			}, nil
		},
	}

	tb := &Toolbox{Conversations: messenger}
	ctx := application.WithConversationID(context.Background(), "conv_source")

	out, err := tb.Execute(ctx, "conversation", []byte(`{"op":"list"}`))
	if err != nil || out == "" {
		t.Fatalf("conversation list: out=%q err=%v", out, err)
	}

	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"search","query":"target"}`))
	if err != nil || out == "" {
		t.Fatalf("conversation search: out=%q err=%v", out, err)
	}

	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"send","id":"conv_target","content":"Hello agent"}`))
	if err != nil {
		t.Fatalf("conversation send error: %v", err)
	}
	if out != "Message delivered to conversation `conv_target`" {
		t.Fatalf("send out = %q", out)
	}

	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"info","id":"conv_target"}`))
	if err != nil {
		t.Fatalf("conversation info error: %v", err)
	}
	if !strings.Contains(out, "turn_count: 3") || !strings.Contains(out, "id: conv_target") {
		t.Fatalf("info out = %q", out)
	}

	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"read","id":"conv_target","start":0,"end":0}`))
	if err != nil {
		t.Fatalf("conversation read error: %v", err)
	}
	if !strings.Contains(out, `"content":"hello"`) {
		t.Fatalf("read out = %q", out)
	}

	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"search","id":"conv_target","query":"hello"}`))
	if err != nil {
		t.Fatalf("conversation scoped search error: %v", err)
	}
	if !strings.Contains(out, "scope: messages") || !strings.Contains(out, `"snippet":"hello"`) {
		t.Fatalf("scoped search out = %q", out)
	}
}
