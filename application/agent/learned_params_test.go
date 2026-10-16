package agent

import (
	"errors"
	"testing"

	"nusashell/domain"
)

// fakeLearnedParamStore is an in-memory LearnedParamStore for testing.
type fakeLearnedParamStore struct {
	registry *domain.LearnedParamRegistry
	saves    int
}

func (f *fakeLearnedParamStore) Load() *domain.LearnedParamRegistry {
	if f.registry == nil {
		return domain.NewLearnedParamRegistry()
	}
	return f.registry
}

func (f *fakeLearnedParamStore) Save(r *domain.LearnedParamRegistry) error {
	f.saves++
	f.registry = r
	return nil
}

func TestIsLearnable400(t *testing.T) {
	if !IsLearnable400(&domain.ProviderError{StatusCode: 400, Err: errors.New("bad request")}) {
		t.Error("400 should be learnable")
	}
	if IsLearnable400(&domain.ProviderError{StatusCode: 429, Err: errors.New("rate limit")}) {
		t.Error("429 should not be learnable")
	}
	if IsLearnable400(&domain.ProviderError{StatusCode: 500, Err: errors.New("server error")}) {
		t.Error("500 should not be learnable")
	}
	if IsLearnable400(errors.New("plain error")) {
		t.Error("non-ProviderError should not be learnable")
	}
}

func TestExtractErrBody(t *testing.T) {
	if got := ExtractErrBody(nil); got != "" {
		t.Errorf("ExtractErrBody(nil) = %q, want empty", got)
	}
	if got := ExtractErrBody(errors.New("plain")); got != "plain" {
		t.Errorf("ExtractErrBody(plain) = %q, want plain", got)
	}
	if got := ExtractErrBody(&domain.ProviderError{StatusCode: 400, Err: errors.New("unsupported")}); got != "unsupported" {
		t.Errorf("ExtractErrBody(upstream) = %q, want unsupported", got)
	}
}
