package ai

import (
	"testing"
)

func TestNewProviderHTTPClientHasNoBodyTimeout(t *testing.T) {
	client := newProviderHTTPClient()
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want 0 so SSE bodies can outlive 60s", client.Timeout)
	}
}
