// Package ttserr provides the sentinel error type used by the online and
// offline speech backends for input-validation failures. Extracted from the
// application root so the speech generators depend on a small leaf package
// instead of the whole application package.
package ttserr

// ErrTTS wraps a human-readable speech-generation validation message.
func ErrTTS(msg string) error { return &ttsError{msg} }

type ttsError struct{ msg string }

func (e *ttsError) Error() string { return e.msg }
