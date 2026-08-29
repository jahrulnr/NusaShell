package application

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
	if !isLearnable400(&UpstreamError{StatusCode: 400, Err: errors.New("bad request")}) {
		t.Error("400 should be learnable")
	}
	if isLearnable400(&UpstreamError{StatusCode: 429, Err: errors.New("rate limit")}) {
		t.Error("429 should not be learnable")
	}
	if isLearnable400(&UpstreamError{StatusCode: 500, Err: errors.New("server error")}) {
		t.Error("500 should not be learnable")
	}
	if isLearnable400(errors.New("plain error")) {
		t.Error("non-UpstreamError should not be learnable")
	}
}

func TestExtractErrBody(t *testing.T) {
	if got := extractErrBody(nil); got != "" {
		t.Errorf("extractErrBody(nil) = %q, want empty", got)
	}
	if got := extractErrBody(errors.New("plain")); got != "plain" {
		t.Errorf("extractErrBody(plain) = %q, want plain", got)
	}
	if got := extractErrBody(&UpstreamError{StatusCode: 400, Err: errors.New("unsupported")}); got != "unsupported" {
		t.Errorf("extractErrBody(upstream) = %q, want unsupported", got)
	}
}
