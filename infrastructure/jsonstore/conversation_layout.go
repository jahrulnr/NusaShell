package jsonstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"nusashell/domain"
)

// Conversation storage layout
//
// One conversation owns one directory under <dir>/conversations:
//
//	conversations/<conversationID>/
//	    index.jsonl   live (un-compacted) transcript epoch
//	    chunk/        archived pre-compaction chunks: chunk-<n>.json
//	    acp/          terminal ACP / internal-delegate runs: <runID>.json
//	    operation/    per-turn net unified diff patches: <runID>.patch
//	    plan.md       mirrored todo brief (owned by TodoStore)
//
// index.jsonl is the conversation record as JSON Lines: the first line is the
// conversation metadata (the Conversation object without its transcript) and
// every following line is one domain.Message. The record stream is
// append-shaped, greppable, and survives a torn tail: the loader skips an
// unparsable message line instead of dropping the whole conversation.
//
// Legacy installs kept the same data as flat siblings of the directory
// (conversations/<id>.json, <id>.chunks/, <id>.acp/). Those are converted once
// at boot by migrateLegacyConversationLayout.
const (
	conversationsDirName  = "conversations"
	conversationIndexName = "index.jsonl"
	chunkDirName          = "chunk"
	acpDirName            = "acp"
	operationDirName      = "operation"
	// legacyChunkDirSuffix / legacyACPDirSuffix name the pre-migration
	// sidecar directories: conversations/<id>.chunks and <id>.acp.
	legacyChunkDirSuffix = ".chunks"
	legacyACPDirSuffix   = ".acp"
)

// conversationDirPath resolves the directory owning one conversation inside
// conversationsDir. Unsafe IDs are rejected before any filesystem call.
func conversationDirPath(conversationsDir, id string) (string, error) {
	if err := safeSegment(id); err != nil {
		return "", fmt.Errorf("conversation %w", err)
	}
	return filepath.Join(conversationsDir, id), nil
}

// conversationSubdirPath resolves one sidecar directory of a conversation
// (chunk, acp, operation).
func conversationSubdirPath(conversationsDir, id, sub string) (string, error) {
	dir, err := conversationDirPath(conversationsDir, id)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, sub), nil
}

// conversationIndexPath resolves the JSONL transcript path for a conversation.
func conversationIndexPath(conversationsDir, id string) (string, error) {
	return conversationSubdirPath(conversationsDir, id, conversationIndexName)
}

// conversationChunkDir resolves the compaction-chunk directory of a conversation.
func conversationChunkDir(conversationsDir, id string) (string, error) {
	return conversationSubdirPath(conversationsDir, id, chunkDirName)
}

// conversationACPDir resolves the ACP run-record directory of a conversation.
func conversationACPDir(conversationsDir, id string) (string, error) {
	return conversationSubdirPath(conversationsDir, id, acpDirName)
}

// conversationOperationDir resolves the turn-patch directory of a conversation.
func conversationOperationDir(conversationsDir, id string) (string, error) {
	return conversationSubdirPath(conversationsDir, id, operationDirName)
}

// conversationHeader is the first index.jsonl record: the conversation
// metadata without the transcript, which follows as its own lines. The
// shadowing Messages field keeps that shape an encoding detail instead of a
// domain rule: the outer field (depth 0) dominates the embedded one, so the
// transcript is omitted even though the copied conversation still carries it.
type conversationHeader struct {
	domain.Conversation
	Messages []domain.Message `json:"Messages,omitempty"`
}

// encodeConversationJSONL renders a conversation as JSON Lines: one metadata
// record followed by one record per message. Every line ends with a newline,
// including the last.
func encodeConversationJSONL(c *domain.Conversation) ([]byte, error) {
	if c == nil {
		return nil, errors.New("conversation is nil")
	}
	var sb bytes.Buffer
	line, err := json.Marshal(conversationHeader{Conversation: *c})
	if err != nil {
		return nil, err
	}
	sb.Write(line)
	sb.WriteByte('\n')
	for i := range c.Messages {
		line, err := json.Marshal(c.Messages[i])
		if err != nil {
			return nil, err
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.Bytes(), nil
}

// decodeConversationJSONL parses an index.jsonl transcript. The first
// non-empty line is the conversation metadata; every later non-empty line is a
// message. Unparsable message lines are counted and skipped (a torn tail from
// a crash must not hide the rest of the conversation); an unparsable or
// missing metadata line is an error.
func decodeConversationJSONL(b []byte) (*domain.Conversation, int, error) {
	lines := strings.Split(string(b), "\n")
	head := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		head = i
		break
	}
	if head < 0 {
		return nil, 0, errors.New("empty conversation transcript")
	}
	var c domain.Conversation
	if err := json.Unmarshal([]byte(lines[head]), &c); err != nil {
		return nil, 0, err
	}
	skipped := 0
	for _, line := range lines[head+1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m domain.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			skipped++
			continue
		}
		c.Messages = append(c.Messages, m)
	}
	return &c, skipped, nil
}

// layoutMigration reports what a legacy-layout conversion moved.
type layoutMigration struct {
	Conversations int
	ChunkDirs     int
	ACPDirs       int
	Skipped       int
}

func (m layoutMigration) moved() bool {
	return m.Conversations > 0 || m.ChunkDirs > 0 || m.ACPDirs > 0
}

// layoutMigrationMu serializes conversions when the conversation store and
// the ACP run store both trigger the migration in one process.
var layoutMigrationMu sync.Mutex

// migrateLegacyConversationLayout converts legacy flat conversation files and
// sidecar directories into the per-conversation folder layout:
//
//	<id>.json    → <id>/index.jsonl   (record converted, original removed)
//	<id>.chunks/ → <id>/chunk/        (directory renamed)
//	<id>.acp/    → <id>/acp/          (directory renamed)
//
// It is idempotent and non-destructive: a legacy file whose new-layout record
// already exists, and a conflicting file inside a sidecar directory, are left
// in place (warned and retried on the next boot) rather than deleted or
// overwritten. Files that are not conversations (todos.json, acp_runs.jsonl,
// retired <id>.meta.json sidecars) are ignored.
func migrateLegacyConversationLayout(conversationsDir string) layoutMigration {
	layoutMigrationMu.Lock()
	defer layoutMigrationMu.Unlock()

	var report layoutMigration
	entries, err := os.ReadDir(conversationsDir)
	if err != nil {
		return report
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			switch {
			case strings.HasSuffix(name, legacyChunkDirSuffix):
				id := strings.TrimSuffix(name, legacyChunkDirSuffix)
				if moveLegacySidecarDir(conversationsDir, id, name, chunkDirName) {
					report.ChunkDirs++
				} else {
					report.Skipped++
				}
			case strings.HasSuffix(name, legacyACPDirSuffix):
				id := strings.TrimSuffix(name, legacyACPDirSuffix)
				if moveLegacySidecarDir(conversationsDir, id, name, acpDirName) {
					report.ACPDirs++
				} else {
					report.Skipped++
				}
			}
			continue
		}
		id, ok := legacyConversationFileID(name)
		if !ok {
			continue
		}
		if migrateLegacyConversationFile(conversationsDir, id, filepath.Join(conversationsDir, name)) {
			report.Conversations++
		} else {
			report.Skipped++
		}
	}
	return report
}

// legacyConversationFileID extracts the conversation ID from a legacy flat
// transcript name (conv_<id>.json). Fixed sidecars such as
// conv_<id>.meta.json are not transcripts: their base name still contains a
// dot after stripping the extension, so they are rejected.
func legacyConversationFileID(name string) (string, bool) {
	base, ok := strings.CutSuffix(name, ".json")
	if !ok || !strings.HasPrefix(base, "conv_") || strings.Contains(base, ".") {
		return "", false
	}
	return base, true
}

// migrateLegacyConversationFile converts one flat transcript into
// <id>/index.jsonl and removes the original only after the new record is
// durably in place. Returns false (leaving everything untouched) when the
// conversion cannot be done safely.
func migrateLegacyConversationFile(conversationsDir, id, legacyPath string) bool {
	indexPath, err := conversationIndexPath(conversationsDir, id)
	if err != nil {
		slog.Warn("legacy conversation file has an unusable id and was left in place", "file", legacyPath, "error", err)
		return false
	}
	if _, err := os.Stat(indexPath); err == nil {
		// Both layouts exist: the folder record is authoritative and the
		// legacy file is not conversation state we wrote. Leave it alone.
		slog.Warn("legacy conversation file left in place: folder record already exists", "file", legacyPath)
		return false
	}
	b, err := os.ReadFile(legacyPath)
	if err != nil {
		slog.Warn("unreadable legacy conversation file was left in place", "file", legacyPath, "error", err)
		return false
	}
	var c domain.Conversation
	if err := json.Unmarshal(b, &c); err != nil {
		slog.Warn("unparsable legacy conversation file was left in place", "file", legacyPath, "error", err)
		return false
	}
	if c.ID != id {
		slog.Warn("legacy conversation file id does not match its name and was left in place",
			"file", legacyPath, "id", c.ID)
		return false
	}
	out, err := encodeConversationJSONL(&c)
	if err != nil {
		slog.Warn("legacy conversation file could not be encoded and was left in place", "file", legacyPath, "error", err)
		return false
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		slog.Warn("legacy conversation folder could not be created", "file", legacyPath, "error", err)
		return false
	}
	if err := atomicWrite(indexPath, out); err != nil {
		slog.Warn("legacy conversation file could not be migrated", "file", legacyPath, "error", err)
		return false
	}
	if err := os.Remove(legacyPath); err != nil {
		slog.Warn("migrated conversation file could not be removed", "file", legacyPath, "error", err)
		return false
	}
	return true
}

// moveLegacySidecarDir moves conversations/<id><suffix>/ to
// conversations/<id>/<sub>/ and removes the legacy directory once it is empty.
// A plain rename is used when the target does not exist yet; otherwise every
// non-conflicting file moves individually and conflicts stay on both sides.
func moveLegacySidecarDir(conversationsDir, id, name, sub string) bool {
	if err := safeSegment(id); err != nil {
		slog.Warn("legacy conversation sidecar has an unusable id and was left in place", "dir", name, "error", err)
		return false
	}
	legacyDir := filepath.Join(conversationsDir, name)
	convDir := filepath.Join(conversationsDir, id)
	target := filepath.Join(convDir, sub)

	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := os.MkdirAll(convDir, 0o755); err == nil {
			if err := os.Rename(legacyDir, target); err == nil {
				return true
			}
		}
		// Fall through to the per-file move below.
	}

	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		slog.Warn("legacy conversation sidecar could not be read", "dir", legacyDir, "error", err)
		return false
	}
	moved := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		dst := filepath.Join(target, e.Name())
		if _, err := os.Stat(dst); err == nil {
			slog.Warn("legacy conversation sidecar file left in place: target exists",
				"file", filepath.Join(legacyDir, e.Name()))
			continue
		}
		if err := os.MkdirAll(target, 0o755); err != nil {
			slog.Warn("legacy conversation sidecar target could not be created", "dir", target, "error", err)
			return moved > 0
		}
		if err := os.Rename(filepath.Join(legacyDir, e.Name()), dst); err != nil {
			slog.Warn("legacy conversation sidecar file could not be moved", "file", e.Name(), "error", err)
			return moved > 0
		}
		moved++
	}
	if rest, err := os.ReadDir(legacyDir); err == nil && len(rest) == 0 {
		_ = os.Remove(legacyDir)
	}
	return moved > 0
}
