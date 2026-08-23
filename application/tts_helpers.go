package application

import (
	"strings"
)

// tts.go helpers: settings resolution + input validation shared by the
// online and offline speech backends.

// NormalizeTTSFormat maps empty/unknown formats to mp3 and validates.
func normalizeTTSFormat(format string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "mp3":
		return "mp3", nil
	case "wav":
		return "wav", nil
	case "opus":
		return "opus", nil
	default:
		return "", errTTS("format must be one of: mp3, wav, opus")
	}
}

func errTTS(msg string) error { return &ttsError{msg} }

type ttsError struct{ msg string }

func (e *ttsError) Error() string { return e.msg }
