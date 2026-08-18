package ci

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nusashell/domain"
)

func TestHTTPNotifierPostsRunCompletion(t *testing.T) {
	var got webhookPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %s, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewHTTPNotifier()
	now := time.Now().UTC()
	run := &domain.WorkflowRun{
		ID:     "run_1",
		Status: domain.StatusSuccess,
		Definition: domain.WorkflowDefinition{Name: "test-workflow"},
		StartedAt:  &now,
		FinishedAt: &now,
	}
	if err := n.NotifyRunCompleted(context.Background(), srv.URL, run); err != nil {
		t.Fatalf("NotifyRunCompleted: %v", err)
	}
	if got.RunID != "run_1" {
		t.Errorf("run_id = %q, want run_1", got.RunID)
	}
	if got.Status != "success" {
		t.Errorf("status = %q, want success", got.Status)
	}
	if got.Workflow != "test-workflow" {
		t.Errorf("workflow = %q, want test-workflow", got.Workflow)
	}
}

func TestHTTPNotifierFailsOn4xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	n := NewHTTPNotifier()
	run := &domain.WorkflowRun{ID: "run_2", Status: domain.StatusFailed}
	err := n.NotifyRunCompleted(context.Background(), srv.URL, run)
	if err == nil || !strings.Contains(err.Error(), "400") {
		t.Fatalf("want 400 error, got %v", err)
	}
}

func TestHTTPNotifierSkipsEmptyURL(t *testing.T) {
	n := NewHTTPNotifier()
	run := &domain.WorkflowRun{ID: "run_3"}
	// Notifier with empty URL should return an error from http.NewRequest,
	// not panic.
	err := n.NotifyRunCompleted(context.Background(), "", run)
	if err == nil {
		t.Fatal("want error for empty URL")
	}
}
