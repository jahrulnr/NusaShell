package application

import "fmt"

// ErrUnsupportedProvider is returned when a stored provider kind has no adapter.
type ErrUnsupportedProvider struct {
	Kind string
}

func (e *ErrUnsupportedProvider) Error() string {
	return fmt.Sprintf("unsupported provider kind: %s", e.Kind)
}
