package agent

import (
	"errors"

	"nusashell/domain"
)

// ExtractErrBody returns the upstream error message string from an
// domain.ProviderError, or the plain error string when not wrapped.
func ExtractErrBody(err error) string {
	if err == nil {
		return ""
	}
	var upstream *domain.ProviderError
	if errors.As(err, &upstream) && upstream.Err != nil {
		return upstream.Err.Error()
	}
	return err.Error()
}

// IsLearnable400 reports whether err is an HTTP 400 that the learning
// classifier should inspect. We only learn from 400s (not 429/5xx — those
// are transient and handled by the retry loop).
func IsLearnable400(err error) bool {
	var upstream *domain.ProviderError
	if !errors.As(err, &upstream) {
		return false
	}
	return upstream.StatusCode == 400
}
