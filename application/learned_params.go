package application

import (
	"errors"

	"nusashell/domain"
)

// extractErrBody returns the upstream error message string from an
// domain.ProviderError, or the plain error string when not wrapped.
func extractErrBody(err error) string {
	if err == nil {
		return ""
	}
	var upstream *domain.ProviderError
	if errors.As(err, &upstream) && upstream.Err != nil {
		return upstream.Err.Error()
	}
	return err.Error()
}

// isLearnable400 reports whether err is an HTTP 400 that the learning
// classifier should inspect. We only learn from 400s (not 429/5xx — those
// are transient and handled by the retry loop).
func isLearnable400(err error) bool {
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.StatusCode == 400
}
