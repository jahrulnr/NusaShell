package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestHTTPStatusCode(t *testing.T) {
	if got := HTTPStatusCode(nil); got != 0 {
		t.Fatalf("nil = %d, want 0", got)
	}
	if got := HTTPStatusCode(errors.New("plain")); got != 0 {
		t.Fatalf("plain = %d, want 0", got)
	}
	inner := &ProviderError{Kind: KindHTTPStatus, StatusCode: 404, Err: fmt.Errorf("provider returned HTTP 404: <!DOCTYPE html>")}
	if got := HTTPStatusCode(fmt.Errorf("wrap: %w", inner)); got != 404 {
		t.Fatalf("wrapped 404 = %d, want 404", got)
	}
	if got := HTTPStatusCode(&ProviderError{Kind: KindConnect, Err: errors.New("dial")}); got != 0 {
		t.Fatalf("connect = %d, want 0", got)
	}
}
