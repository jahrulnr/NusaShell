package tools

import (
	"testing"
	"time"
)

func TestSearchwireConfigsUseExtendedWebRequestTimeout(t *testing.T) {
	if got := SearchwireConfigFromProviders(nil, nil).Timeout; got != 60*time.Second {
		t.Fatalf("startup searchwire timeout = %s, want 60s", got)
	}
	if got := SearchwireSearchConfig(nil).Timeout; got != 60*time.Second {
		t.Fatalf("per-call searchwire timeout = %s, want 60s", got)
	}
}
