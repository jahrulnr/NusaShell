package application

import (
	"context"
	"errors"
	"testing"
)

func TestSearchCodexWebHonorsCancellationBeforeCredentialLookup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := &App{}
	_, err := app.SearchCodexWeb(ctx, CodexSearchRequest{ProviderID: "codex", Query: "query"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
