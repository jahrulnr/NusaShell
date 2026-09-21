package tools

// Read-shaped file tools: file_read, file_list, file_info. These only
// inspect the filesystem — no path locks, no atomic writes, no turndiff
// deltas.

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	clock "nusashell/pkg/time"
)

func executeFileRead(args map[string]any) (bool, string, error) {
	path := fileArgStr(args, "path")
	if strings.TrimSpace(path) == "" {
		return true, "", fmt.Errorf("path is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	fileSize := len(data)
	fileHash := fileSHA256(data)
	whitespace := inspectFileWhitespace(data)
	meta := map[string]any{"total_lines": fileLineCount(data), "sha256": fileHash}
	addFileWhitespaceMeta(meta, whitespace)

	startLine := fileArgInt(args, "start_line", 0)
	endLine := fileArgInt(args, "end_line", 0)
	lineMode := startLine > 0 || endLine > 0
	offset := 0
	firstLine, lastLine := 0, 0
	if lineMode {
		if startLine < 1 {
			startLine = 1
		}
		if endLine > 0 && endLine < startLine {
			return true, "", fmt.Errorf("end_line (%d) must be >= start_line (%d)", endLine, startLine)
		}
		data, firstLine, lastLine = fileLineSlice(data, startLine, endLine)
		meta["start_line"] = firstLine
		meta["end_line"] = lastLine
	} else {
		offset = fileArgInt(args, "offset_bytes", 0)
		if offset < 0 {
			offset = 0
		}
		if offset > len(data) {
			offset = len(data)
		}
		data = data[offset:]
	}
	// Binary sniff runs before the budget so a huge binary file still
	// reports `binary: true` instead of a generic metadata-only stub.
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		meta["binary"] = true
		meta["size"] = len(data)
		meta["file_bytes"] = fileSize
		return true, yamlMD(meta, "[binary file — not rendered]"), nil
	}

	requested := fileArgInt(args, "max_bytes", 0)
	if requested > fileContentMaxBytes {
		requested = 0 // out of range behaves like "not set"
	}
	targeted := lineMode || offset > 0 || requested > 0
	maxBytes := requested
	switch {
	case targeted && maxBytes <= 0:
		maxBytes = fileReadDefaultMaxBytes
	case !targeted:
		maxBytes = gradedReadBudget(fileSize)
	}
	meta["file_bytes"] = fileSize
	if maxBytes == 0 {
		// Blind read of a huge file: return the coordinates, not a body.
		// The model picks a slice with start_line/end_line or max_bytes —
		// 32KiB of head would cost more attention than it is worth and
		// could be mistaken for the whole file.
		meta["bytes"] = 0
		meta["truncated"] = true
		meta["hint"] = fmt.Sprintf("file is %d bytes (%d lines); metadata only — read a slice with start_line/end_line or max_bytes, or grep for the region", fileSize, meta["total_lines"])
		return true, yamlMD(meta, ""), nil
	}

	truncated := false
	if len(data) > maxBytes {
		data = data[:maxBytes]
		truncated = true
	}
	meta["bytes"] = len(data)
	if !targeted && fileSize > fileReadLargeTierBytes {
		meta["hint"] = fmt.Sprintf("large file (%d bytes, %d lines) — only a small head is included; prefer grep or a line range over paging", fileSize, meta["total_lines"])
	}
	if offset > 0 {
		meta["offset_bytes"] = offset
	}
	if truncated {
		meta["truncated"] = true
		if lineMode {
			meta["next_start_line"] = fileNextStartLine(data, firstLine)
		} else {
			meta["next_offset_bytes"] = offset + len(data)
		}
	}
	body := string(data)
	if fileArgBool(args, "show_whitespace") {
		body = escapeVisibleWhitespace(body)
	}
	return true, yamlMD(meta, body), nil
}

func executeFileList(args map[string]any) (bool, string, error) {
	path := fileArgStr(args, "path")
	if path == "" {
		path = "."
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return true, "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	now := clock.NewTime().Time()
	lines := make([]string, 0, len(entries))
	var totalSize int64
	for _, e := range entries {
		if len(lines) >= fileListEntryLimit {
			break
		}
		info, ierr := e.Info()
		if ierr != nil {
			continue
		}
		totalSize += info.Size()
		lines = append(lines, lsLine(info, now))
	}
	meta := map[string]any{"count": len(entries), "total": humanSize(totalSize)}
	if len(entries) > fileListEntryLimit {
		meta["truncated"] = true
		meta["shown"] = len(lines)
	}
	return true, yamlMD(meta, strings.Join(lines, "\n")), nil
}

func executeFileInfo(args map[string]any) (bool, string, error) {
	path := fileArgStr(args, "path")
	if strings.TrimSpace(path) == "" {
		return true, "", fmt.Errorf("path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		// Missing paths are not an error: report exists=false so the
		// caller can branch without parsing error strings (this absorbs
		// the old file_exists contract).
		if os.IsNotExist(err) {
			return true, yamlBlock(map[string]any{"exists": false}), nil
		}
		return true, "", err
	}
	meta := map[string]any{
		"exists":   true,
		"name":     info.Name(),
		"size":     info.Size(),
		"dir":      info.IsDir(),
		"mode":     info.Mode().String(),
		"modified": clock.NewTime(info.ModTime()).Format(time.RFC3339),
	}
	return true, yamlBlock(meta), nil
}
