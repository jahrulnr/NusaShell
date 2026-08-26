package application

import ()

// tts.go helpers: settings resolution + input validation shared by the
// online and offline speech backends.

func errTTS(msg string) error { return &ttsError{msg} }

type ttsError struct{ msg string }

func (e *ttsError) Error() string { return e.msg }
