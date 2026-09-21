package tools

// Write-shaped file tools: file_write, file_patch. Both mutate file content
// through writeFileAtomic under a per-path lock and record a turndiff delta
// (or an inexact marker) so the UI can render the change.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"nusashell/domain/turndiff"
)

func executeFileWrite(ctx context.Context, args map[string]any) (bool, string, error) {
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
	// Share the path lock with file_patch so a parallel overwrite cannot
	// interleave with an in-flight same-path patch RMW.
	unlock := lockFilePath(path)
	defer unlock()
	pre := readPathText(path)
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		recordInexact(ctx)
		return true, "", err
	}
	if !pre.exact || !isTrackableText(data) {
		recordInexact(ctx)
	} else {
		var overwritten *string
		if pre.exists {
			overwritten = turndiff.StringPtr(pre.text)
		}
		recordDelta(ctx, turndiff.AddFile(path, string(data), overwritten))
	}
	meta := map[string]any{"bytes": len(data), "sha256": fileSHA256(data), "written": true}
	addFileWhitespaceMeta(meta, inspectFileWhitespace(data))
	return true, yamlMD(meta, ""), nil
}

func executeFilePatch(ctx context.Context, args map[string]any) (bool, string, error) {
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
	if strings.TrimSpace(path) == "" {
		return true, "", fmt.Errorf("path is required")
	}
	if oldStr == "" {
		return true, "", fmt.Errorf("old_string is required")
	}
	// Serialize same-path RMW so parallel tool calls apply incrementally
	// against the latest content instead of racing last-writer-wins.
	unlock := lockFilePath(path)
	defer unlock()
	raw, err := os.ReadFile(path)
	if err != nil {
		return true, "", err
	}
	s := string(raw)
	currentSHA := fileSHA256(raw)
	whitespace := inspectFileWhitespace(raw)
	count := strings.Count(s, oldStr)
	healed := false
	var healedStart, healedEnd int
	autoHeal := true
	if value, ok := args["auto_heal"].(bool); ok {
		autoHeal = value
	}
	if count == 0 && autoHeal {
		span, candidates, candidatesLines := findWhitespacePatch(raw, oldStr)
		// findWhitespacePatch early-returns on the second candidate, so
		// candidates is 0, 1, or 2: 1 heals, 2 is ambiguous, and 0 falls
		// through to the rich PATCH_CONTEXT_NOT_FOUND diagnostic below.
		switch candidates {
		case 1:
			healed = true
			healedStart, healedEnd = span.start, span.end
			newStr = preserveFileLineEndings(newStr, whitespace.lineEnding)
		case 2:
			return true, "", fmt.Errorf("PATCH_CONTEXT_AMBIGUOUS: old_string has no exact match and matches %d locations after whitespace normalization in %s (current_sha256=%s %s candidate_lines=%v); re-read the file or pass auto_heal=false", candidates, path, currentSHA, whitespace.summary(), candidatesLines)
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
		recordInexact(ctx)
		return true, "", err
	}
	if !isTrackableText(raw) || !isTrackableText([]byte(out)) {
		recordInexact(ctx)
	} else {
		recordDelta(ctx, turndiff.UpdateFile(path, s, out, nil, nil))
	}
	return true, yamlMD(meta, ""), nil
}
