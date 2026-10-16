package tools

// Path-management file tools: file_mkdir, file_delete, file_move,
// file_copy. These create, remove, and relocate filesystem entries; the
// mutating ones serialize on path locks and record turndiff deltas like the
// content writers.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain/turndiff"
	"nusashell/pkg/yamlmd"
)

func executeFileMkdir(args map[string]any) (bool, string, error) {
	path := fileArgStr(args, "path")
	if strings.TrimSpace(path) == "" {
		return true, "", fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return true, "", err
	}
	return true, yamlmd.Block(map[string]any{"created": true}), nil
}

func executeFileDelete(ctx context.Context, args map[string]any) (bool, string, error) {
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
	// Serialize the read-then-mutate section with other same-path file
	// ops (write/patch/move/copy) so a concurrent patch cannot resurrect
	// a file this delete already removed.
	unlock := lockFilePaths(path)
	defer unlock()
	pre := readPathText(path)
	if err := os.RemoveAll(path); err != nil {
		turndiff.Record(ctx, turndiff.Inexact())
		return true, "", err
	}
	if pre.dir || !pre.exact {
		turndiff.Record(ctx, turndiff.Inexact())
	} else {
		turndiff.Record(ctx, turndiff.DeleteFile(path, pre.text))
	}
	return true, yamlmd.Block(map[string]any{"deleted": true}), nil
}

func executeFileMove(ctx context.Context, args map[string]any) (bool, string, error) {
	src := fileArgStr(args, "source")
	dst := fileArgStr(args, "destination")
	if src == "" || dst == "" {
		return true, "", fmt.Errorf("source and destination are required")
	}
	// Lock both source and destination (sorted acquisition order, so
	// file_move(a,b) and file_move(b,a) cannot deadlock) and keep the
	// read-then-mutate section inside the lock so a concurrent same-path
	// patch/delete cannot race the rename.
	unlock := lockFilePaths(src, dst)
	defer unlock()
	srcPre := readPathText(src)
	dstPre := readPathText(dst)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return true, "", err
	}
	if err := renameWithRetry(src, dst); err != nil {
		// Fall back to copy+delete (e.g. cross-device rename).
		if cerr := copyTree(src, dst); cerr != nil {
			turndiff.Record(ctx, turndiff.Inexact())
			return true, "", err
		}
		if rerr := os.RemoveAll(src); rerr != nil {
			turndiff.Record(ctx, turndiff.Inexact())
			return true, "", rerr
		}
	}
	if srcPre.dir || !srcPre.exact || (dstPre.exists && !dstPre.exact) {
		turndiff.Record(ctx, turndiff.Inexact())
	} else {
		var overwritten *string
		if dstPre.exists {
			overwritten = turndiff.StringPtr(dstPre.text)
		}
		turndiff.Record(ctx, turndiff.UpdateFile(src, srcPre.text, srcPre.text, turndiff.StringPtr(dst), overwritten))
	}
	return true, yamlmd.Block(map[string]any{"moved": true}), nil
}

func executeFileCopy(ctx context.Context, args map[string]any) (bool, string, error) {
	src := fileArgStr(args, "source")
	dst := fileArgStr(args, "destination")
	if src == "" || dst == "" {
		return true, "", fmt.Errorf("source and destination are required")
	}
	// Lock both source and destination so a concurrent same-path
	// patch/delete/move cannot race the read-then-copy section.
	unlock := lockFilePaths(src, dst)
	defer unlock()
	srcPre := readPathText(src)
	dstPre := readPathText(dst)
	if err := copyTree(src, dst); err != nil {
		turndiff.Record(ctx, turndiff.Inexact())
		return true, "", err
	}
	if srcPre.dir || !srcPre.exact || (dstPre.exists && !dstPre.exact) {
		turndiff.Record(ctx, turndiff.Inexact())
	} else {
		var overwritten *string
		if dstPre.exists {
			overwritten = turndiff.StringPtr(dstPre.text)
		}
		turndiff.Record(ctx, turndiff.AddFile(dst, srcPre.text, overwritten))
	}
	return true, yamlmd.Block(map[string]any{"copied": true}), nil
}
