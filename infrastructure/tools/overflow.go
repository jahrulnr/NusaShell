package tools

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"nusashell/infrastructure/nusatemp"
)

// toolInlineMaxBytes is the Cursor-like in-band budget for grep, exec,
// web_fetch, web_search, and docs. Matches file_read's default so the
// model continues with the same offset_bytes contract.
const toolInlineMaxBytes = 32 << 10

func toolOverflowDir() (string, error) {
	return nusatemp.Dir()
}

func sanitizeToolFileBase(tool string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(tool) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func writeToolOverflow(tool, body string) (string, error) {
	dir, err := toolOverflowDir()
	if err != nil {
		return "", err
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	name := sanitizeToolFileBase(tool) + "-" + hex.EncodeToString(id[:]) + ".txt"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func createToolOverflowFile(tool string) (*os.File, string, error) {
	dir, err := toolOverflowDir()
	if err != nil {
		return nil, "", err
	}
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, "", err
	}
	name := sanitizeToolFileBase(tool) + "-" + hex.EncodeToString(id[:]) + ".txt"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

func clipUTF8Prefix(s string, n int) string {
	if n <= 0 || s == "" {
		return ""
	}
	if len(s) <= n {
		return s
	}
	head := s[:n]
	for !utf8.ValidString(head) && len(head) > 0 {
		head = head[:len(head)-1]
	}
	return head
}

// capToolOutput keeps body in-band when it fits the inline budget.
// Oversized bodies are written in full to
// filepath.Join(os.TempDir(), "nusashell", "<tool>-<id>.txt") and the
// in-band result is the YAML header plus the first 32KiB, with
// overflow_path / overflow_bytes / next_offset_bytes so file_read can
// page the rest.
func capToolOutput(tool string, meta map[string]any, body string) string {
	if meta == nil {
		meta = map[string]any{}
	}
	if len(body) <= toolInlineMaxBytes {
		return yamlMD(meta, body)
	}
	path, err := writeToolOverflow(tool, body)
	if err != nil {
		meta["overflow_error"] = err.Error()
	} else {
		meta["overflow_path"] = path
	}
	head := clipUTF8Prefix(body, toolInlineMaxBytes)
	meta["truncated"] = true
	meta["overflow_bytes"] = len(body)
	meta["next_offset_bytes"] = len(head)
	return yamlMD(meta, head)
}

func attachSpillMeta(meta map[string]any, path string, bytes int64, keep bool) {
	if meta == nil {
		return
	}
	if !keep {
		if path != "" {
			_ = os.Remove(path)
		}
		return
	}
	meta["truncated"] = true
	meta["overflow_path"] = path
	meta["overflow_bytes"] = bytes
	// In-band exec output is a head+tail sample, not a prefix of the
	// spill file. file_read the overflow from offset 0 for the complete log.
	meta["next_offset_bytes"] = 0
}
