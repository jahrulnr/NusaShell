package tools

import (
	"context"
	"testing"
	"time"

	"nusashell/application"
)

type stubConversationMessenger struct {
	listFn   func(currID string, limit, offset int) (int, []application.ConversationSummaryDTO, error)
	searchFn func(currID, query string, limit, offset int) (int, []application.ConversationSummaryDTO, error)
	sendFn   func(currID, targetID, content string) error
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

func TestToolboxConversationOps(t *testing.T) {
	messenger := &stubConversationMessenger{
		listFn: func(currID string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
			return 1, []application.ConversationSummaryDTO{
				{ID: "conv_target", Title: "Target Room", Summary: "Working on feature X", Status: "idle", UpdatedAt: time.Now().Format(time.RFC3339)},
			}, nil
		},
		searchFn: func(currID, query string, limit, offset int) (int, []application.ConversationSummaryDTO, error) {
			return 1, []application.ConversationSummaryDTO{
				{ID: "conv_target", Title: "Target Room", Summary: "Working on feature X", Status: "idle", UpdatedAt: time.Now().Format(time.RFC3339)},
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
	}

	tb := &Toolbox{Conversations: messenger}
	ctx := application.WithConversationID(context.Background(), "conv_source")

	// 1. List
	out, err := tb.Execute(ctx, "conversation", []byte(`{"op":"list"}`))
	if err != nil {
		t.Fatalf("conversation list error: %v", err)
	}
	if out == "" {
		t.Fatal("expected output for conversation list")
	}

	// 2. Search
	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"search","query":"target"}`))
	if err != nil {
		t.Fatalf("conversation search error: %v", err)
	}
	if out == "" {
		t.Fatal("expected output for conversation search")
	}

	// 3. Send
	out, err = tb.Execute(ctx, "conversation", []byte(`{"op":"send","id":"conv_target","content":"Hello agent"}`))
	if err != nil {
		t.Fatalf("conversation send error: %v", err)
	}
	wantOut := "Message delivered to conversation `conv_target`"
	if out != wantOut {
		t.Fatalf("send out = %q, want %q", out, wantOut)
	}
}
