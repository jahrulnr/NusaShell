package tools

// Built-in file CRUD operations (the mainland). These run natively inside
// the Go binary via the os stdlib — no MCP hop, no child process.
// Writes are atomic: temp file in the same directory, then rename.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"nusashell/application"
)

const (
	fileReadDefaultMaxBytes = 32 << 10 // default read cap (token economy)
	fileContentMaxBytes     = 10 << 20 // hard cap for written content (10 MiB)
	fileListEntryLimit      = 2000     // safety cap for list entries
)

func fileToolInfos() []application.ToolInfo {
	return []application.ToolInfo{
		{Name: "file_read", Description: "Read a text file from disk. Returns up to max_bytes (default 32768); continue with offset_bytes when truncated. Metadata reports the complete file's line ending, tab count, carriage-return count, and trailing-whitespace lines. Set show_whitespace=true for a copy-safe inspection view with invisible whitespace rendered visibly. Binary files are reported, not dumped.", InputSchema: obj("object", props("path", str("Absolute file path"), "offset_bytes", intSchema("Byte offset to start reading from (default 0)"), "max_bytes", intSchema("Maximum bytes returned (default 32768)"), "show_whitespace", obj("boolean", nil)), "path")},
		{Name: "file_write", Description: "Create or overwrite a text file atomically (temp file in the same directory, then rename). Parent directories are created automatically. encoding=escaped decodes visible whitespace markers such as \\t, \\r, \\n, and \\\\ without normalizing line endings.", InputSchema: obj("object", props("path", str("Absolute file path"), "content", str("File content (UTF-8, max 10 MB)"), "encoding", strEnum("Content encoding: utf8 (default), escaped visible-whitespace text, or base64", "utf8", "escaped", "base64")), "path", "content")},
		{Name: "file_patch", Description: "Replace an exact substring in a file. Fails unless old_string matches exactly once; disambiguate multiple matches with occurrence (1-based). After an exact miss, auto-heal defaults to one unique whitespace-equivalent match; set auto_heal=false for exact-only behavior. Use encoding=escaped when copying visible \\t/\\r/\\n markers from file_read(show_whitespace=true), so CRLF and tabs are matched exactly without normalization. Use expected_sha256 from file_read to fail closed when the file changed since it was read. Success returns the new sha256 and reports healed=true when whitespace recovery was used; ambiguous whitespace matches never write and report the current version, while no-match failures include whitespace statistics and a nearby excerpt with invisible characters rendered visibly. Use preview=true to see the result without writing.", InputSchema: obj("object", props("path", str("Absolute file path"), "old_string", str("Exact text to replace"), "new_string", str("Replacement text (may be empty to delete)"), "encoding", strEnum("String encoding: utf8 (default) or escaped visible-whitespace text", "utf8", "escaped"), "auto_heal", obj("boolean", nil), "occurrence", intSchema("1-based occurrence to replace when old_string appears multiple times"), "expected_sha256", str("Optional SHA-256 returned by file_read; fail if the file changed"), "preview", obj("boolean", nil)), "path", "old_string", "new_string")},
		{Name: "file_list", Description: "List a directory's entries with type, size, and modified time.", InputSchema: obj("object", props("path", str("Absolute directory path")))},
		{Name: "file_mkdir", Description: "Create a directory including any missing parents.", InputSchema: obj("object", props("path", str("Absolute directory path")), "path")},
		{Name: "file_delete", Description: "Delete a file or directory. Directories require recursive=true when not empty. Irreversible.", InputSchema: obj("object", props("path", str("Absolute path to delete"), "recursive", obj("boolean", nil)), "path")},
		{Name: "file_move", Description: "Move or rename a file/directory; overwrites an existing destination. Falls back to copy+delete across filesystems.", InputSchema: obj("object", props("source", str("Absolute source path"), "destination", str("Absolute destination path")), "source", "destination")},
		{Name: "file_copy", Description: "Copy a file or directory recursively.", InputSchema: obj("object", props("source", str("Absolute source path"), "destination", str("Absolute destination path")), "source", "destination")},
		{Name: "file_info", Description: "Get metadata for a path (exists, size, permissions, type, modification time). Does NOT error on missing paths — returns exists=false.", InputSchema: obj("object", props("path", str("Absolute file or directory path")), "path")},
		{Name: "grep", Description: "Search file contents with regex. Built on Go regexp (RE2 syntax — no backreferences). " +
			"Filters files by glob_pattern, returns matching lines with optional context_lines. " +
			"output_mode: content (matching lines + context), files_with_matches (just filenames), count (match count per file). " +
			"Set show_whitespace=true in content mode to render tabs and carriage returns visibly. " +
			"Skips .git, node_modules, vendor, and *.min.js/*.min.css/*.map. Content lines are clipped at 200 bytes. " +
			"In-band results cap at ~32KiB; overflow_path is the full body in the platform temp dir — continue with file_read (next_offset_bytes). " +
			"Prefer this over exec+shell grep — structured output, no process spawn, works without rg installed.",
			InputSchema: obj("object", props(
				"pattern", str("Regular expression to search for (RE2 syntax)"),
				"path", str("Directory or file to search in"),
				"glob_pattern", str("Glob filter for file paths (e.g. \"*.go\", \"**/*.tsx\"). Empty = all files"),
				"output_mode", strEnum("Result format: content (default), files_with_matches, count", "content", "files_with_matches", "count"),
				"context_lines", intSchema("Lines of context before and after each match (default 0, max 10)"),
				"case_insensitive", obj("boolean", nil),
				"max_results", intSchema("Max number of matches to return (default 100)"),
				"show_whitespace", obj("boolean", nil),
			), "pattern", "path")},
		{Name: "find_file", Description: "Find files by glob pattern. Supports ** for recursive directory matching " +
			"(e.g. \"**/*.go\" matches any .go file at any depth) and brace expansion " +
			"(e.g. \"*.{go,ts,py}\"). Skips .git, node_modules, and vendor directories. " +
			"Returns matching file paths sorted alphabetically.",
			InputSchema: obj("object", props(
				"pattern", str("Glob pattern (e.g. \"**/*.tsx\", \"*.go\", \"*.{go,ts}\")"),
				"path", str("Directory to search in (default current directory)"),
			), "pattern")},
		{Name: "show", Description: "Render a file from disk in the UI. op=html reads an HTML file and " +
			"displays it in a sandboxed iframe (for prototypes, dashboards, visualizations, " +
			"minigames — write the file first with file_write, then show it). op=image reads " +
			"an image file and displays it inline. op=audio reads an audio file (mp3, wav, ogg, " +
			"m4a) and displays an inline player. op=video reads a video file (mp4, webm, " +
			"mov, avi) and displays an inline player. Use file_write to create the file, " +
			"file_patch to update it, file_read to inspect it — show only handles display.",
			InputSchema: obj("object", props(
				"op", strEnum("Display type: html (iframe), image (inline), audio (inline player), or video (inline player)", "html", "image", "audio", "video"),
				"path", str("Absolute path to the file to display"),
				"width", intSchema("Iframe width in pixels (html only, default 720)"),
				"height", intSchema("Iframe height in pixels (html only, default 400)"),
			), "op", "path")},
	}
}

// executeFileTool handles the file_* built-ins. Returns handled=false for
// names it does not own so the caller can fall through to other handlers.
func executeFileTool(name string, argsJSON []byte) (bool, string, error) {
	var args map[string]any
	_ = json.Unmarshal(argsJSON, &args)

	switch name {
	case "grep":
		return executeGrep(argsJSON)
	case "find_file":
		return executeFindFile(argsJSON)
	case "show":
		return executeShow(argsJSON)
	case "file_read":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return true, "", err
		}
		fileHash := fileSHA256(data)
		whitespace := inspectFileWhitespace(data)
		offset := fileArgInt(args, "offset_bytes", 0)
		if offset < 0 {
			offset = 0
		}
		if offset > len(data) {
			offset = len(data)
		}
		data = data[offset:]
		maxBytes := fileArgInt(args, "max_bytes", fileReadDefaultMaxBytes)
		if maxBytes <= 0 || maxBytes > fileContentMaxBytes {
			maxBytes = fileReadDefaultMaxBytes
		}
		truncated := false
		if len(data) > maxBytes {
			data = data[:maxBytes]
			truncated = true
		}
		head := data
		if len(head) > 1024 {
			head = head[:1024]
		}
		if bytes.IndexByte(head, 0) >= 0 {
			meta := map[string]any{"binary": true, "size": len(data), "sha256": fileHash}
			addFileWhitespaceMeta(meta, whitespace)
			return true, yamlMD(meta, "[binary file — not rendered]"), nil
		}
		meta := map[string]any{"bytes": len(data), "sha256": fileHash}
		addFileWhitespaceMeta(meta, whitespace)
		if offset > 0 {
			meta["offset_bytes"] = offset
		}
		if truncated {
			meta["truncated"] = true
			meta["next_offset_bytes"] = offset + len(data)
		}
		body := string(data)
		if fileArgBool(args, "show_whitespace") {
			body = escapeVisibleWhitespace(body)
		}
		return true, yamlMD(meta, body), nil

	case "file_write":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		content := fileArgStr(args, "content")
		var data []byte
		switch enc := fileArgStr(args, "encoding"); enc {
		case "", "utf8":
			data = []byte(content)
		case "base64":
			decoded, err := base64.StdEncoding.DecodeString(content)
			if err != nil {
				return true, "", fmt.Errorf("invalid base64 content: %w", err)
			}
			data = decoded
		case "escaped":
			decoded, err := decodeVisibleWhitespace(content)
			if err != nil {
				return true, "", fmt.Errorf("invalid escaped content: %w", err)
			}
			data = []byte(decoded)
		default:
			return true, "", fmt.Errorf("unknown encoding %q (use utf8, escaped, or base64)", enc)
		}
		if len(data) > fileContentMaxBytes {
			return true, "", fmt.Errorf("content exceeds %d bytes", fileContentMaxBytes)
		}
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			return true, "", err
		}
		meta := map[string]any{"bytes": len(data), "sha256": fileSHA256(data), "written": true}
		addFileWhitespaceMeta(meta, inspectFileWhitespace(data))
		return true, yamlMD(meta, ""), nil

	case "file_patch":
		path := fileArgStr(args, "path")
		encoding := fileArgStr(args, "encoding")
		oldStr, err := decodeFileText(fileArgStr(args, "old_string"), encoding, "old_string")
		if err != nil {
			return true, "", err
		}
		newStr, err := decodeFileText(fileArgStr(args, "new_string"), encoding, "new_string")
		if err != nil {
			return true, "", err
		}
		expectedSHA := strings.ToLower(strings.TrimSpace(fileArgStr(args, "expected_sha256")))
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		if oldStr == "" {
			return true, "", fmt.Errorf("old_string is required")
		}
		if expectedSHA != "" {
			if len(expectedSHA) != sha256.Size*2 {
				return true, "", fmt.Errorf("expected_sha256 must be a %d-character hexadecimal SHA-256", sha256.Size*2)
			}
			if _, err := hex.DecodeString(expectedSHA); err != nil {
				return true, "", fmt.Errorf("expected_sha256 must be a %d-character hexadecimal SHA-256", sha256.Size*2)
			}
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return true, "", err
		}
		s := string(raw)
		currentSHA := fileSHA256(raw)
		whitespace := inspectFileWhitespace(raw)
		if expectedSHA != "" && expectedSHA != currentSHA {
			return true, "", fmt.Errorf("FILE_CHANGED_SINCE_READ: path=%s expected_sha256=%s current_sha256=%s current_bytes=%d %s; re-read the file before retrying", path, expectedSHA, currentSHA, len(raw), whitespace.summary())
		}
		count := strings.Count(s, oldStr)
		healed := false
		var healedStart, healedEnd int
		autoHeal := true
		if value, ok := args["auto_heal"].(bool); ok {
			autoHeal = value
		}
		if count == 0 && autoHeal {
			span, candidates := findWhitespacePatch(raw, oldStr)
			switch candidates {
			case 1:
				healed = true
				healedStart, healedEnd = span.start, span.end
				newStr = preserveFileLineEndings(newStr, whitespace.lineEnding)
			case 2:
				return true, "", fmt.Errorf("PATCH_CONTEXT_AMBIGUOUS: old_string has no exact match and matches multiple locations after whitespace normalization in %s (current_sha256=%s %s); re-read the file or pass auto_heal=false", path, currentSHA, whitespace.summary())
			}
		}
		if count == 0 && !healed {
			return true, "", patchContextError(path, raw, oldStr, currentSHA)
		}
		var out string
		if healed {
			out = s[:healedStart] + newStr + s[healedEnd:]
		} else {
			switch {
			case count == 1:
				out = strings.Replace(s, oldStr, newStr, 1)
			default:
				occ := fileArgInt(args, "occurrence", 0)
				if occ < 1 || occ > count {
					return true, "", fmt.Errorf("old_string matches %d times in %s; pass occurrence (1-%d) to disambiguate", count, path, count)
				}
				idx := -1
				for n := occ; n > 0; n-- {
					j := strings.Index(s[idx+1:], oldStr)
					if j < 0 {
						return true, "", fmt.Errorf("old_string not found in %s", path)
					}
					idx += j + 1
				}
				out = s[:idx] + newStr + s[idx+len(oldStr):]
			}
		}
		meta := map[string]any{"bytes": len(out), "path": path, "replaced": 1, "sha256": fileSHA256([]byte(out))}
		addFileWhitespaceMeta(meta, inspectFileWhitespace([]byte(out)))
		if healed {
			meta["healed"] = true
			meta["match_mode"] = "whitespace"
		}
		if fileArgBool(args, "preview") {
			meta["preview"] = true
			previewBody := out
			if encoding == "escaped" {
				previewBody = escapeVisibleWhitespace(previewBody)
			}
			return true, yamlMD(meta, previewBody), nil
		}
		if err := writeFileAtomic(path, []byte(out), 0o644); err != nil {
			return true, "", err
		}
		return true, yamlMD(meta, ""), nil

	case "file_list":
		path := fileArgStr(args, "path")
		if path == "" {
			path = "."
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return true, "", err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		now := time.Now()
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

	case "file_mkdir":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return true, "", err
		}
		return true, yamlBlock(map[string]any{"created": true}), nil

	case "file_delete":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return true, "", err
		}
		if info.IsDir() && !fileArgBool(args, "recursive") {
			if children, derr := os.ReadDir(path); derr == nil && len(children) > 0 {
				return true, "", fmt.Errorf("%s is a non-empty directory; pass recursive=true to delete it", path)
			}
		}
		if err := os.RemoveAll(path); err != nil {
			return true, "", err
		}
		return true, yamlBlock(map[string]any{"deleted": true}), nil

	case "file_move":
		src := fileArgStr(args, "source")
		dst := fileArgStr(args, "destination")
		if src == "" || dst == "" {
			return true, "", fmt.Errorf("source and destination are required")
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return true, "", err
		}
		if err := renameWithRetry(src, dst); err != nil {
			// Fall back to copy+delete (e.g. cross-device rename).
			if cerr := copyTree(src, dst); cerr != nil {
				return true, "", err
			}
			if rerr := os.RemoveAll(src); rerr != nil {
				return true, "", rerr
			}
		}
		return true, yamlBlock(map[string]any{"moved": true}), nil

	case "file_copy":
		src := fileArgStr(args, "source")
		dst := fileArgStr(args, "destination")
		if src == "" || dst == "" {
			return true, "", fmt.Errorf("source and destination are required")
		}
		if err := copyTree(src, dst); err != nil {
			return true, "", err
		}
		return true, yamlBlock(map[string]any{"copied": true}), nil

	case "file_info":
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
			"modified": info.ModTime().UTC().Format(time.RFC3339),
		}
		return true, yamlBlock(meta), nil
	}
	return false, "", nil
}

func fileArgStr(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func fileArgInt(args map[string]any, key string, def int) int {
	if f, ok := args[key].(float64); ok {
		return int(f)
	}
	return def
}

func fileArgBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func decodeFileText(value, encoding, field string) (string, error) {
	switch encoding {
	case "", "utf8":
		return value, nil
	case "escaped":
		decoded, err := decodeVisibleWhitespace(value)
		if err != nil {
			return "", fmt.Errorf("invalid escaped %s: %w", field, err)
		}
		return decoded, nil
	default:
		return "", fmt.Errorf("unknown encoding %q (use utf8 or escaped)", encoding)
	}
}

// escapeVisibleWhitespace is intentionally an inspection format, not a line
// ending normalizer. It makes the bytes most likely to disappear in a
// rendered tool result visible while leaving newlines as actual newlines so
// the result remains easy to scan and can be decoded by file_write/file_patch.
func escapeVisibleWhitespace(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\t", "\\t")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func decodeVisibleWhitespace(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 >= len(s) {
			return "", fmt.Errorf("trailing backslash")
		}
		i++
		switch s[i] {
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		case '\\':
			b.WriteByte('\\')
		default:
			return "", fmt.Errorf("unsupported escape \\\\%c (use \\t, \\r, \\n, or \\\\)", s[i])
		}
	}
	return b.String(), nil
}

type patchSpan struct {
	start int
	end   int
}

type normalizedPatchText struct {
	text  string
	start []int
	end   []int
}

// normalizePatchText is used only after an exact patch miss. It makes line
// endings and horizontal whitespace comparable while retaining a mapping to
// the original byte offsets, so an auto-healed edit still replaces the exact
// bytes that were present in the file.
func normalizePatchText(s string) normalizedPatchText {
	var normalized normalizedPatchText
	var b strings.Builder
	b.Grow(len(s))
	appendByte := func(value byte, start, end int) {
		b.WriteByte(value)
		normalized.start = append(normalized.start, start)
		normalized.end = append(normalized.end, end)
	}

	for i := 0; i < len(s); {
		switch s[i] {
		case '\r':
			end := i + 1
			if end < len(s) && s[end] == '\n' {
				end++
			}
			appendByte('\n', i, end)
			i = end
		case '\n':
			appendByte('\n', i, i+1)
			i++
		case ' ', '\t':
			start := i
			for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
				i++
			}
			appendByte(' ', start, i)
		default:
			appendByte(s[i], i, i+1)
			i++
		}
	}
	normalized.text = b.String()
	return normalized
}

func findWhitespacePatch(raw []byte, oldString string) (patchSpan, int) {
	if !strings.ContainsAny(string(raw), "\t\r") && !strings.ContainsAny(oldString, "\t\r") {
		return patchSpan{}, 0
	}
	needle := normalizePatchText(oldString)
	if needle.text == "" || !patchTextHasNonWhitespace(needle.text) {
		return patchSpan{}, 0
	}
	haystack := normalizePatchText(string(raw))
	if len(needle.text) > len(haystack.text) {
		return patchSpan{}, 0
	}

	var match patchSpan
	count := 0
	for from := 0; from <= len(haystack.text)-len(needle.text); {
		idx := strings.Index(haystack.text[from:], needle.text)
		if idx < 0 {
			break
		}
		start := from + idx
		end := start + len(needle.text)
		count++
		if count == 1 {
			match = patchSpan{start: haystack.start[start], end: haystack.end[end-1]}
		}
		if count >= 2 {
			return match, count
		}
		from = end
	}
	return match, count
}

func patchTextHasNonWhitespace(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\n' {
			return true
		}
	}
	return false
}

func preserveFileLineEndings(s, lineEnding string) string {
	if lineEnding != "crlf" && lineEnding != "cr" && lineEnding != "lf" {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	switch lineEnding {
	case "crlf":
		return strings.ReplaceAll(s, "\n", "\r\n")
	case "cr":
		return strings.ReplaceAll(s, "\n", "\r")
	default:
		return s
	}
}

type fileWhitespaceInfo struct {
	lineEnding              string
	tabs                    int
	carriageReturns         int
	trailingWhitespaceLines int
}

func inspectFileWhitespace(data []byte) fileWhitespaceInfo {
	var info fileWhitespaceInfo
	crlf, loneCR, loneLF := 0, 0, 0
	for i, c := range data {
		switch c {
		case '\r':
			info.carriageReturns++
			if i+1 < len(data) && data[i+1] == '\n' {
				crlf++
			} else {
				loneCR++
			}
		case '\n':
			if i == 0 || data[i-1] != '\r' {
				loneLF++
			}
		case '\t':
			info.tabs++
		}
	}
	switch {
	case crlf > 0 && loneCR == 0 && loneLF == 0:
		info.lineEnding = "crlf"
	case crlf == 0 && loneCR > 0 && loneLF == 0:
		info.lineEnding = "cr"
	case crlf == 0 && loneCR == 0 && loneLF > 0:
		info.lineEnding = "lf"
	case crlf == 0 && loneCR == 0 && loneLF == 0:
		info.lineEnding = "none"
	default:
		info.lineEnding = "mixed"
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			info.trailingWhitespaceLines++
		}
	}
	return info
}

func addFileWhitespaceMeta(meta map[string]any, info fileWhitespaceInfo) {
	meta["line_ending"] = info.lineEnding
	meta["tabs"] = info.tabs
	meta["carriage_returns"] = info.carriageReturns
	meta["trailing_whitespace_lines"] = info.trailingWhitespaceLines
}

func (info fileWhitespaceInfo) summary() string {
	return fmt.Sprintf("line_ending=%s tabs=%d carriage_returns=%d trailing_whitespace_lines=%d", info.lineEnding, info.tabs, info.carriageReturns, info.trailingWhitespaceLines)
}

func fileSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// patchContextError keeps a failed exact-match edit actionable without
// dumping the whole file into the conversation. The longest non-empty line
// from old_string is a useful, bounded anchor when one surrounding line has
// changed since the model's last read.
func patchContextError(path string, raw []byte, oldString, currentSHA string) error {
	const maxContextLineBytes = 240

	whitespace := inspectFileWhitespace(raw)
	message := fmt.Sprintf(
		"PATCH_CONTEXT_NOT_FOUND: old_string not found in %s (old_string_bytes=%d current_bytes=%d current_sha256=%s %s); re-read the file and copy old_string verbatim before retrying; if using visible whitespace markers, pass encoding=escaped; nearby_context:",
		path, len(oldString), len(raw), currentSHA, whitespace.summary(),
	)
	lines := strings.Split(string(raw), "\n")
	anchorLine := patchAnchorLine(lines, oldString)
	if anchorLine == 0 {
		end := 3
		if end > len(lines) {
			end = len(lines)
		}
		var b strings.Builder
		b.WriteString(message + " no matching anchor line; file_head:")
		for lineNo := 1; lineNo <= end; lineNo++ {
			line := escapeVisibleWhitespace(lines[lineNo-1])
			if len(line) > maxContextLineBytes {
				line = clipUTF8Prefix(line, maxContextLineBytes) + "…"
			}
			fmt.Fprintf(&b, "\n  line %d: %s", lineNo, line)
		}
		return errors.New(b.String())
	}

	start := anchorLine - 2
	if start < 1 {
		start = 1
	}
	end := anchorLine + 2
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	b.WriteString(message)
	for lineNo := start; lineNo <= end; lineNo++ {
		line := escapeVisibleWhitespace(lines[lineNo-1])
		if len(line) > maxContextLineBytes {
			line = clipUTF8Prefix(line, maxContextLineBytes) + "…"
		}
		fmt.Fprintf(&b, "\n  line %d: %s", lineNo, line)
	}
	return errors.New(b.String())
}

func patchAnchorLine(lines []string, oldString string) int {
	anchors := make([]string, 0, len(strings.Split(oldString, "\n")))
	for _, line := range strings.Split(oldString, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) != "" && len(strings.TrimSpace(line)) >= 3 {
			anchors = append(anchors, line)
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		return len(anchors[i]) > len(anchors[j])
	})
	for _, anchor := range anchors {
		for i, line := range lines {
			line = strings.TrimSuffix(line, "\r")
			if strings.Contains(line, anchor) {
				return i + 1
			}
		}
	}
	return 0
}

// Injected effects so rename-retry behavior stays deterministically testable.
var (
	renameFn   = os.Rename
	sleepFn    = time.Sleep
	renameGoos = runtime.GOOS
)

const (
	renameMaxAttempts = 4
	renameBaseBackoff = 10 * time.Millisecond
)

// renameWithRetry renames from→to, briefly retrying when Windows reports a
// transient sharing violation. Antivirus scanners and search indexers
// routinely hold a just-closed temp file for a few milliseconds, which makes
// an otherwise-valid os.Rename fail with ERROR_SHARING_VIOLATION,
// ERROR_LOCK_VIOLATION, or ERROR_ACCESS_DENIED. A short bounded backoff
// absorbs that window without masking permanent failures.
func renameWithRetry(from, to string) error {
	var err error
	for attempt := 0; attempt < renameMaxAttempts; attempt++ {
		if attempt > 0 {
			sleepFn(renameBaseBackoff << (attempt - 1))
		}
		err = renameFn(from, to)
		if err == nil || !isTransientRenameErr(err) {
			return err
		}
	}
	return err
}

// isTransientRenameErr reports whether err is one of the transient Windows
// file-locking errors worth retrying once or twice.
func isTransientRenameErr(err error) bool {
	if renameGoos != "windows" {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case 5, 32, 33: // ERROR_ACCESS_DENIED, ERROR_SHARING_VIOLATION, ERROR_LOCK_VIOLATION
		return true
	default:
		return false
	}
}

// writeFileAtomic writes data to a temp file in the target directory and
// renames it into place, so a crash never leaves a partial file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".nusashell-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	if err := renameWithRetry(tmpName, path); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// copyTree copies a file or directory recursively (regular files only;
// symlinks are followed).
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	}
	if err := os.MkdirAll(dst, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
