package main

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"nusashell/application"
	"nusashell/domain"
	"nusashell/infrastructure/mcpclient"
)

type panicNotificationWorkflowStore struct {
	started chan struct{}
	once    sync.Once
}

func (s *panicNotificationWorkflowStore) Put(context.Context, *domain.WorkflowDefinition) error {
	return nil
}

func (s *panicNotificationWorkflowStore) Get(context.Context, string) (*domain.WorkflowDefinition, error) {
	return nil, nil
}

func (s *panicNotificationWorkflowStore) List(context.Context) ([]*domain.WorkflowDefinition, error) {
	s.once.Do(func() { close(s.started) })
	panic("simulated notification workflow store panic")
}

func (s *panicNotificationWorkflowStore) Delete(context.Context, string) error { return nil }

type panicRecoveryLogStore struct {
	done chan struct{}
	once sync.Once
}

func (s *panicRecoveryLogStore) Append(entry *domain.LogEntry) {
	if strings.Contains(entry.Message, "goroutine panic recovered") {
		s.once.Do(func() { close(s.done) })
	}
}

func (*panicRecoveryLogStore) List(string, int) []*domain.LogEntry { return nil }
func (*panicRecoveryLogStore) Clear()                              {}

type notificationEventStore struct{}

func (notificationEventStore) PutEvent(context.Context, *domain.Event) error { return nil }
func (notificationEventStore) RecordDelivery(context.Context, string, string, string, string, time.Time) (bool, error) {
	return true, nil
}
func (notificationEventStore) ListEvents(context.Context, int) ([]*domain.Event, error) {
	return nil, nil
}

func TestHandleMCPNotificationIsAsyncAndRecoversIngestPanic(t *testing.T) {
	workflowStore := &panicNotificationWorkflowStore{started: make(chan struct{})}
	logs := &panicRecoveryLogStore{done: make(chan struct{})}
	app := &application.App{Logs: logs}
	autoSvc := &application.Automation{
		Sched: &application.AutomationScheduler{Workflows: workflowStore, Events: notificationEventStore{}},
	}
	n := mcp.JSONRPCNotification{
		Notification: mcp.Notification{
			Method: mcpclient.NotificationMessage,
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{
				"plugin":     "nusashell.telegram",
				"event":      "message",
				"chat_id":    "520213916",
				"message_id": "42",
				"from_me":    false,
			}},
		},
	}

	called := make(chan struct{})
	go func() {
		handleMCPNotification(app, autoSvc, "nusashell.telegram", n)
		close(called)
	}()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("MCP notification handler blocked the caller")
	}
	select {
	case <-workflowStore.started:
	case <-time.After(time.Second):
		t.Fatal("notification was not ingested")
	}
	select {
	case <-logs.done:
	case <-time.After(time.Second):
		t.Fatal("ingest panic was not recovered by App.GoSafe")
	}
}
