package repoconversation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jahrulnr/nusashell/infrastructure/domain/aggregate"
	apperror "github.com/jahrulnr/nusashell/infrastructure/error"
)

// Fork copies a room's stored history into a new one: every sealed segment and
// the live tail, with the message attachments and tool-output files they
// reference. dst names the copy and supplies its bindings; the stored
// messages come from srcID, which is only read.
//
// The copy is assembled in a staging directory and renamed into place, then
// its record is written last — so Info, List, and Load report the new room
// only once every stored line and file is there, and a failed fork leaves the
// source untouched and nothing half-copied behind.
func (r *ConversationRepository) Fork(ctx context.Context, srcID string, dst *aggregate.Conversation) error {
	if err := validID(srcID); err != nil {
		return err
	}
	if err := validID(dst.ID()); err != nil {
		return err
	}
	if dst.ID() == srcID {
		return apperror.New(400, "a fork needs a new conversation id", nil)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	unlock, err := r.lockStore()
	if err != nil {
		return err
	}
	defer unlock()

	src, err := r.readMeta(srcID)
	if err != nil {
		return err
	}
	srcDir, dstDir := r.convDir(srcID), r.convDir(dst.ID())
	if _, err := os.Stat(dstDir); err == nil {
		return apperror.New(409, "conversation "+dst.ID()+" already exists", nil)
	}
	stage, err := os.MkdirTemp(r.path, dst.ID()+".forking-")
	if err != nil {
		return err
	}
	// A published copy has moved out of the staging name; a failed one is
	// dropped whole.
	defer os.RemoveAll(stage)

	for i := range src.SealedSegments {
		name := fmt.Sprintf("%d.jsonl", i)
		if err := copyStream(filepath.Join(srcDir, name), filepath.Join(stage, name), srcDir, dstDir); err != nil {
			return err
		}
	}
	if err := copyStream(filepath.Join(srcDir, activeFile), filepath.Join(stage, activeFile), srcDir, dstDir); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(srcDir, toolOutputDir), filepath.Join(stage, toolOutputDir)); err != nil {
		return err
	}
	if err := os.Rename(stage, dstDir); err != nil {
		return err
	}

	// The record lands last: until it exists the room is invisible to Info,
	// List, and Load. The counters describe exactly the streams just copied.
	m := metaFor(dst)
	m.CreatedAt = time.Now()
	m.MessageCount, m.ActiveCount, m.SealedSegments = src.MessageCount, src.ActiveCount, src.SealedSegments
	return r.writeMeta(dst.ID(), m)
}

// copyStream rebuilds one stored stream for the copy. A line that references a
// file inside the source room — tool output a message or a call carries — is
// repointed at the copy's own directory; every other line is written exactly
// as it was stored, patches included.
func copyStream(src, dst, srcDir, dstDir string) error {
	lines, err := readLines(src)
	if err != nil {
		return err
	}
	from := "file://" + filepath.Join(srcDir, toolOutputDir) + string(filepath.Separator)
	to := "file://" + filepath.Join(dstDir, toolOutputDir) + string(filepath.Separator)
	out := make([][]byte, 0, len(lines))
	for _, line := range lines {
		if !bytes.Contains(line, []byte(from)) {
			out = append(out, line)
			continue
		}
		rewritten, err := repointLine(line, from, to)
		if err != nil {
			return fmt.Errorf("fork %s: %w", filepath.Base(src), err)
		}
		out = append(out, rewritten)
	}
	return writeFileAtomic(dst, joinLines(out), 0o600)
}

// repointLine rewrites the stored file references of one line, leaving every
// other field of the record intact.
func repointLine(line []byte, from, to string) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber() // counters keep their exact text instead of a float64 round trip
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("corrupt line: %w", err)
	}
	repointURIs(node, from, to)
	return json.Marshal(node)
}

// repointURIs walks a decoded line for attachment uri values under the source
// room's directory and moves them to the copy's.
func repointURIs(node any, from, to string) {
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if s, ok := value.(string); ok && key == "uri" && strings.HasPrefix(s, from) {
				v[key] = to + strings.TrimPrefix(s, from)
				continue
			}
			repointURIs(value, from, to)
		}
	case []any:
		for _, item := range v {
			repointURIs(item, from, to)
		}
	}
}

// copyTree copies the files a room spilled — its tool output — byte for byte,
// so the copy's history points at its own files instead of the source's. A
// missing source tree holds nothing to copy.
func copyTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}
	for _, e := range entries {
		// The spill folder holds plain files this store wrote. Anything else
		// there was planted, and a copy must not follow it.
		if !e.Type().IsRegular() {
			return fmt.Errorf("fork: %s is not a regular file", filepath.Join(src, e.Name()))
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
