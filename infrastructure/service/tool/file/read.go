package toolfile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jahrulnr/nusashell/infrastructure/domain/entity"
	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	"github.com/jahrulnr/nusashell/infrastructure/domain/service"
	"github.com/jahrulnr/nusashell/infrastructure/domain/valueobject"
)

// FileReadTool reads a text file under the turn's workspace, or one an
// absolute path names elsewhere — the constructor carries the schema and
// validates the default root; Execute resolves the call input through the
// injected workspace repository, whose local implementation confines
// symlink traversal from whatever folder the call opened.
type FileReadTool struct {
	dir    string
	store  drepo.Workspace
	schema valueobject.ToolSchema
}

// NewFileReadTool binds the tool's default root: a relative path resolves
// inside it, an absolute path anchors where it lives. A turn-scoped
// workspace dir overrides it per call.
func NewFileReadTool(store drepo.Workspace, root string) (service.Tool, error) {
	abs, err := checkDir(store, root)
	if err != nil {
		return nil, err
	}
	schema, err := valueobject.NewToolSchema(
		"read_file",
		"Read a UTF-8 text file relative to the workspace root. A file too long for one answer comes back a page at a time: the answer ends with the lines shown and the offset to continue from.",
		json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace root, an absolute path anywhere on disk, or ~/… for the owner's home"},"offset":{"type":"integer","description":"First line to read, 1-based (default 1)"},"limit":{"type":"integer","description":"Most lines to read (default: as many as fit)"}},"required":["path"]}`),
		true,
		enum.ToolPermissionAgent,
	)
	if err != nil {
		return nil, err
	}
	return &FileReadTool{dir: abs, store: store, schema: schema}, nil
}

func (tool *FileReadTool) Schema() valueobject.ToolSchema {
	return tool.schema
}

// readFileMax bounds what one call loads to page through: a larger file
// is a job for exec (sed -n, head, tail).
const readFileMax = 16 << 20

func (tool *FileReadTool) Execute(ctx context.Context, call entity.ToolCall, _ service.ToolEmitter) (result service.ToolResult, retErr error) {
	if err := ctx.Err(); err != nil {
		return service.ToolResult{}, err
	}
	var input struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal([]byte(call.Input()), &input); err != nil {
		return service.ToolResult{}, fmt.Errorf("invalid read_file input: %w", err)
	}
	root, rel, err := callRoot(ctx, tool.store, tool.dir, input.Path)
	if err != nil {
		return service.ToolResult{}, err
	}
	defer func() {
		if closeErr := root.Close(); retErr == nil {
			retErr = closeErr
		}
	}()
	f, info, err := root.OpenRegular(rel)
	if err != nil {
		return service.ToolResult{}, err
	}
	defer func() {
		if closeErr := f.Close(); retErr == nil {
			retErr = closeErr
		}
	}()
	if info.Size() > readFileMax {
		return service.ToolResult{}, fmt.Errorf("%q is too large to page through (%d bytes > %d MiB) — read parts of it with exec (sed -n, head, tail)", input.Path, info.Size(), readFileMax>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, readFileMax+1))
	if err != nil {
		return service.ToolResult{}, err
	}
	if len(data) > readFileMax {
		return service.ToolResult{}, fmt.Errorf("%q is too large to page through (> %d MiB)", input.Path, readFileMax>>20)
	}
	// The schema promises UTF-8 text. Sniff the same window read_media
	// uses and send non-text payloads there — a PNG is not a document.
	if top, _, _ := strings.Cut(http.DetectContentType(data), "/"); top != "text" {
		return service.ToolResult{}, fmt.Errorf("%q is not text — use read_media", input.Path)
	}
	// Text-sniffed but invalid UTF-8 (Latin-1, UTF-16 without BOM, …)
	// reaches the model as replacement characters — the provider JSON
	// encoder swaps bad bytes for U+FFFD without saying so.
	if !utf8.Valid(data) {
		return service.ToolResult{}, fmt.Errorf("%q is not valid UTF-8 — convert it first (e.g. iconv via exec)", input.Path)
	}
	text, err := readPage(string(data), max(input.Offset, 1), input.Limit, call.OutputBudget())
	if err != nil {
		return service.ToolResult{}, fmt.Errorf("%q: %w", input.Path, err)
	}
	return service.ToolResult{Text: text}, nil
}

// readPage answers lines offset… of text (1-based), at most limit of them
// when limit is positive, as many whole lines as fit budget bytes. A
// whole file answers as it is; a part ends with a note naming the lines
// shown and the offset to continue from, so the model pages instead of
// receiving a middle-truncated document. A single line longer than the
// budget is cut, and the note says so.
func readPage(text string, offset, limit, budget int) (string, error) {
	lines := strings.SplitAfter(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if offset == 1 && (limit <= 0 || limit >= total) && len(text) <= budget {
		return text, nil
	}
	if offset > total {
		return "", fmt.Errorf("offset %d is past the end (%d lines)", offset, total)
	}
	const noteRoom = 160
	room := max(budget-noteRoom, 1)
	var b strings.Builder
	end := offset - 1
	cut := false
	for end < total && (limit <= 0 || end-offset+1 < limit) {
		line := lines[end]
		if b.Len()+len(line) > room {
			if b.Len() == 0 {
				// Cut on a rune boundary.
				n := room
				for n > 0 && !utf8.RuneStart(line[n]) {
					n--
				}
				line = line[:n]
				cut = true
				b.WriteString(line)
				end++
			}
			break
		}
		b.WriteString(line)
		end++
	}
	out := b.String()
	if !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	note := fmt.Sprintf("\n[read_file: lines %d-%d of %d", offset, end, total)
	if cut {
		note += "; line " + fmt.Sprint(end) + " is cut — read it with exec"
	}
	if end < total {
		note += fmt.Sprintf(". Continue with offset=%d", end+1)
	}
	return out + note + "]", nil
}
