package toolfile

import "github.com/jahrulnr/nusashell/infrastructure/system/patharg"

// expandPath is the one expansion every file tool applies before placeOf.
// The rule lives in infrastructure/system/patharg because the media tools
// and the HTTP preview open the same kind of path a model wrote; see
// patharg.Expand for what expands (file: URL, a leading ~, Windows %VAR%)
// and what stays literal.
func expandPath(path string) (string, error) { return patharg.Expand(path) }
