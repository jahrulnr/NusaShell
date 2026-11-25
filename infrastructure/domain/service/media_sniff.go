package service

import (
	"net/http"
	"path/filepath"
	"strings"
)

// SniffMediaType names a payload by its bytes, with the file's extension as
// the tie-breaker where the byte sniffer is blind or wrong: an Ogg container
// is audio unless the name says .ogv, an MPEG-4 audio file sniffs as video,
// and some audio containers (aac, flac without a tag) sniff as
// application/octet-stream. One rule for every caller that has to know what a
// file is — the tools that read it and the media tools that hand it to a
// provider — so no two paths can disagree: a voice note that transcribes fine
// must not be refused as an unreadable "file" one layer up.
func SniffMediaType(raw []byte, path string) string {
	t, _, _ := strings.Cut(http.DetectContentType(raw), ";")
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case t == "application/ogg":
		if ext == ".ogv" {
			return "video/ogg"
		}
		return "audio/ogg"
	case t == "video/mp4" && ext == ".m4a":
		return "audio/mp4"
	case t == "video/webm" && ext == ".weba":
		return "audio/webm"
	case t != "application/octet-stream":
		return t
	}
	switch ext {
	case ".m4a":
		return "audio/mp4"
	case ".aac":
		return "audio/aac"
	case ".flac":
		return "audio/flac"
	case ".opus", ".oga":
		return "audio/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".mov":
		return "video/quicktime"
	}
	return t
}
