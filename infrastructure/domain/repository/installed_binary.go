package repository

import "io"

// InstalledBinary is the executable the next launch runs, e.g.
// ~/.local/bin/nusashell. An update replaces it in one rename: the new bytes are
// staged beside it, then committed over it.
type InstalledBinary interface {
	Path() string
	Stage() (BinaryStaging, error)
}

// BinaryStaging is a new executable being written beside the installed one.
// Exactly one of Commit or Discard ends it.
type BinaryStaging interface {
	io.Writer
	// Commit makes the staged file executable and hands it to the installed
	// path. On Unix it renames over the path — a running process keeps the
	// old inode. On Windows the running image is locked, so it stages the
	// file at <path>.new and the restart helper swaps it after this process
	// exits.
	Commit() error
	// Discard drops the staged file and leaves the installed path alone.
	Discard() error
}
