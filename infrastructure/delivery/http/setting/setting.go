// Package setting is the WebUI settings boundary — the effective
// persisted config projected down to whitelisted leaves on read, and a
// PATCH of whitelisted leaves on write. storage.* paths and app metadata never
// cross.
package setting

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/jahrulnr/nusashell/infrastructure/delivery/http/internal/transport"
)

// Reader supplies the effective persisted config document as JSON; the
// projection below reduces it to the browser-safe leaves.
// *setting.SettingApp satisfies it.
type Reader interface {
	Show() ([]byte, error)
}

// Writer patches whitelisted leaves as one write and returns the
// rewritten document. *setting.SettingApp satisfies it.
type Writer interface {
	SetMany(values map[string]string) ([]byte, error)
}

// Deps wires the use cases; Reads and Writes are optional — nil leaves
// the corresponding routes unregistered.
type Deps struct {
	Reads  Reader
	Writes Writer
}

// Register mounts this boundary's routes under /api.
func Register(api *gin.RouterGroup, d Deps) {
	s := &server{reads: d.Reads, writes: d.Writes}
	if d.Reads != nil {
		api.GET("/settings", s.get)
	}
	if d.Writes != nil {
		api.PATCH("/settings", s.patch)
	}
}

type server struct {
	reads  Reader
	writes Writer
}

// leaves is the exact set of config keys the browser may read or write —
// the contract's Settings schema, fixed so a future config field holding
// a path or secret can never cross by accident of projection.
// storage.* (filesystem paths) and app.* (internal metadata) stay out.
var leaves = map[string]bool{
	"server.port":                     true,
	"server.bind":                     true,
	"server.hosts":                    true,
	"server.public_url":               true,
	"conversation.mailbox_cap":        true,
	"conversation.max_turn_steps":     true,
	"conversation.compact_budget_pct": true,
	"conversation.compact_keep_tail":  true,
	"service.header_timeout":          true,
	"service.idle_timeout":            true,
	"agents.idle_timeout":             true,
	"log.max_lines":                   true,
	"log.max_files":                   true,
	"notifications.desktop":           true,
	"memory.enabled":                  true,
	"memory.consolidation":            true,
	"models.threads.agent":            true,
	"models.threads.provider":         true,
	"models.threads.model":            true,
	"models.threads.effort":           true,
	"models.chat.agent":               true,
	"models.chat.provider":            true,
	"models.chat.model":               true,
	"models.chat.effort":              true,
	"models.utility.provider":         true,
	"models.utility.model":            true,
	"models.embedding.provider":       true,
	"models.embedding.model":          true,
	"models.transcription.provider":   true,
	"models.transcription.model":      true,
	"models.image.provider":           true,
	"models.image.model":              true,
	"models.video.provider":           true,
	"models.video.model":              true,
	"models.speech.provider":          true,
	"models.speech.model":             true,
}

// project reduces the persisted config document to the whitelisted
// leaves, keeping their nesting; every section on the path appears even
// when the file holds none of its leaves.
func project(raw []byte) (map[string]any, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for key := range leaves {
		segments := strings.Split(key, ".")
		src, dst := doc, out
		for _, seg := range segments[:len(segments)-1] {
			next, _ := dst[seg].(map[string]any)
			if next == nil {
				next = map[string]any{}
				dst[seg] = next
			}
			dst = next
			src, _ = src[seg].(map[string]any)
		}
		leaf := segments[len(segments)-1]
		if v, ok := src[leaf]; ok {
			dst[leaf] = v
		}
	}
	return out, nil
}

func (s *server) get(c *gin.Context) {
	raw, err := s.reads.Show()
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out, err := project(raw)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, out)
}

// patch writes whitelisted leaves as one write — {"values": {"models.
// utility.provider": "openrouter", "models.utility.model": "…"}} — and
// answers the projected document, so the browser sees exactly what it
// could read.
func (s *server) patch(c *gin.Context) {
	var body struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if !transport.DecodeJSON(c, &body) {
		return
	}
	if len(body.Values) == 0 {
		transport.WriteProblem(c, 400, "invalid_argument", "values must name at least one settings leaf")
		return
	}
	values := make(map[string]string, len(body.Values))
	for key, v := range body.Values {
		if !leaves[key] {
			transport.WriteProblem(c, 400, "invalid_argument", key+" is not a writable settings leaf")
			return
		}
		if v == nil || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			transport.WriteProblem(c, 400, "invalid_argument", key+": value must be a non-null JSON literal")
			return
		}
		values[key] = string(v)
	}
	raw, err := s.writes.SetMany(values)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	out, err := project(raw)
	if err != nil {
		transport.RespondError(c, err)
		return
	}
	c.JSON(200, out)
}
