package tools

// Built-in file CRUD operations (the mainland). These run natively inside
// the Go binary via the os stdlib — no MCP hop, no child process.
// Writes are atomic: temp file in the same directory, then rename.

import (
	"bytes"
	"encoding/base64"
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
		{Name: "file_read", Description: "Read a text file from disk. Returns up to max_bytes (default 32768); continue with offset when truncated. Binary files are reported, not dumped.", InputSchema: obj("object", props("path", str("Absolute file path"), "offset", intSchema("Byte offset to start reading from (default 0)"), "max_bytes", intSchema("Maximum bytes returned (default 32768)")), "path")},
		{Name: "file_write", Description: "Create or overwrite a text file atomically (temp file in the same directory, then rename). Parent directories are created automatically.", InputSchema: obj("object", props("path", str("Absolute file path"), "content", str("File content (UTF-8, max 10 MB)"), "encoding", strEnum("Content encoding: utf8 (default) or base64", "utf8", "base64")), "path", "content")},
		{Name: "file_patch", Description: "Replace an exact substring in a file. Fails unless old_string matches exactly once; disambiguate multiple matches with occurrence (1-based). Use preview=true to see the result without writing.", InputSchema: obj("object", props("path", str("Absolute file path"), "old_string", str("Exact text to replace"), "new_string", str("Replacement text (may be empty to delete)"), "occurrence", intSchema("1-based occurrence to replace when old_string appears multiple times"), "preview", obj("boolean", nil)), "path", "old_string", "new_string")},
		{Name: "file_list", Description: "List a directory's entries with type, size, and modified time.", InputSchema: obj("object", props("path", str("Absolute directory path")))},
		{Name: "file_mkdir", Description: "Create a directory including any missing parents.", InputSchema: obj("object", props("path", str("Absolute directory path")), "path")},
		{Name: "file_delete", Description: "Delete a file or directory. Directories require recursive=true when not empty. Irreversible.", InputSchema: obj("object", props("path", str("Absolute path to delete"), "recursive", obj("boolean", nil)), "path")},
		{Name: "file_move", Description: "Move or rename a file/directory; overwrites an existing destination. Falls back to copy+delete across filesystems.", InputSchema: obj("object", props("source", str("Absolute source path"), "destination", str("Absolute destination path")), "source", "destination")},
		{Name: "file_copy", Description: "Copy a file or directory recursively.", InputSchema: obj("object", props("source", str("Absolute source path"), "destination", str("Absolute destination path")), "source", "destination")},
		{Name: "file_exists", Description: "Check whether a path exists. Does NOT error on missing paths — returns exists=false.", InputSchema: obj("object", props("path", str("Absolute file or directory path")), "path")},
		{Name: "file_info", Description: "Get metadata for a path (size, permissions, type, modification time).", InputSchema: obj("object", props("path", str("Absolute path")), "path")},
		grepToolInfo(),
		findFileToolInfo(),
		showToolInfo(),
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
		offset := fileArgInt(args, "offset", 0)
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
		if fileLooksBinary(data) {
			meta := map[string]any{"binary": true, "size": len(data)}
			return true, yamlMD(meta, "[binary file — not rendered]"), nil
		}
		meta := map[string]any{"bytes": len(data)}
		if offset > 0 {
			meta["offset"] = offset
		}
		if truncated {
			meta["truncated"] = true
			meta["next_offset"] = offset + len(data)
		}
		return true, yamlMD(meta, string(data)), nil

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
		default:
			return true, "", fmt.Errorf("unknown encoding %q (use utf8 or base64)", enc)
		}
		if len(data) > fileContentMaxBytes {
			return true, "", fmt.Errorf("content exceeds %d bytes", fileContentMaxBytes)
		}
		if err := writeFileAtomic(path, data, 0o644); err != nil {
			return true, "", err
		}
		return true, yamlMD(map[string]any{"bytes": len(data), "written": true}, ""), nil

	case "file_patch":
		path := fileArgStr(args, "path")
		oldStr := fileArgStr(args, "old_string")
		newStr := fileArgStr(args, "new_string")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		if oldStr == "" {
			return true, "", fmt.Errorf("old_string is required")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return true, "", err
		}
		s := string(raw)
		count := strings.Count(s, oldStr)
		if count == 0 {
			return true, "", fmt.Errorf("old_string not found in %s", path)
		}
		var out string
		switch {
		case count == 1:
			out = strings.Replace(s, oldStr, newStr, 1)
		default:
			occ := fileArgInt(args, "occurrence", 0)
			if occ < 1 || occ > count {
				return true, "", fmt.Errorf("old_string matches %d times in %s; pass occurrence (1-%d) to disambiguate", count, path, count)
			}
			idx := fileNthIndex(s, oldStr, occ)
			out = s[:idx] + newStr + s[idx+len(oldStr):]
		}
		meta := map[string]any{"replaced": 1}
		if fileArgBool(args, "preview") {
			meta["preview"] = true
			return true, yamlMD(meta, out), nil
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
		items := make([]any, 0, len(entries))
		for _, e := range entries {
			if len(items) >= fileListEntryLimit {
				break
			}
			kind := "file"
			if e.IsDir() {
				kind = "dir"
			}
			item := map[string]any{"name": e.Name(), "type": kind}
			if info, ierr := e.Info(); ierr == nil {
				item["size"] = info.Size()
				item["modified"] = info.ModTime().UTC().Format(time.RFC3339)
			}
			items = append(items, item)
		}
		return true, yamlJSONL(map[string]any{"count": len(entries)}, items), nil

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

	case "file_exists":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		_, serr := os.Stat(path)
		exists := serr == nil || !os.IsNotExist(serr)
		isDir := false
		if serr == nil {
			if info, ierr := os.Stat(path); ierr == nil {
				isDir = info.IsDir()
			}
		}
		return true, yamlBlock(map[string]any{"exists": exists, "is_dir": isDir}), nil

	case "file_info":
		path := fileArgStr(args, "path")
		if strings.TrimSpace(path) == "" {
			return true, "", fmt.Errorf("path is required")
		}
		info, err := os.Stat(path)
		if err != nil {
			return true, "", err
		}
		meta := map[string]any{
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

func fileNthIndex(s, sub string, n int) int {
	i := -1
	for ; n > 0; n-- {
		j := strings.Index(s[i+1:], sub)
		if j < 0 {
			return -1
		}
		i += j + 1
	}
	return i
}

func fileLooksBinary(data []byte) bool {
	head := data
	if len(head) > 1024 {
		head = head[:1024]
	}
	return bytes.IndexByte(head, 0) >= 0
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
