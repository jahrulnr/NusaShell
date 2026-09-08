package agent

import (
	"context"
	"strings"
	"testing"

	"nusashell/domain"
)

type panicProviderStore struct{}

func (panicProviderStore) List() []*domain.Provider {
	panic("simulated provider registry panic")
}

func (panicProviderStore) Get(string) (*domain.Provider, error) { return nil, nil }

func TestRunHeadlessTurnRecoversProviderResolutionPanic(t *testing.T) {
	svc := &Service{Providers: panicProviderStore{}}

	var runErr error
	panicked := false
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				panicked = true
			}
		}()
		_, _, runErr = svc.RunHeadlessTurn(context.Background(), "reply", "", domain.TrustSafe, nil)
	}()
	if panicked {
		t.Fatal("RunHeadlessTurn allowed provider resolution panic to escape")
	}
	if runErr == nil || !strings.Contains(runErr.Error(), "panic") {
		t.Fatalf("RunHeadlessTurn error = %v, want recovered panic diagnostic", runErr)
	}
}
