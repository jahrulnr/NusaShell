// Package archive extracts tar.gz and zip archives into a destination
// directory behind one shared zip-slip guard, replacing the per-installer
// copies. Member names that are absolute, contain a ".." path segment, or
// join outside the destination fail the extraction. Tar link members are
// skipped unless the caller opts into recreation, and recreation is only
// allowed when the resolved link target stays inside the destination.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options controls extraction; nil selects defaults: every member is
// extracted with its recorded mode, link members are skipped, and sizes
// are uncapped.
type Options struct {
	// Prefix limits extraction to members under this slash-separated
	// directory path (e.g. "piper/"). The prefix is stripped from output
	// paths and non-matching members are skipped.
	Prefix string
	// FileMode, when non-zero, forces this permission on extracted
	// regular files instead of the member's recorded mode.
	FileMode os.FileMode
	// RecreateLinks recreates tar symlink/hardlink members whose resolved
	// target stays inside dest; unsafe link targets fail the extraction.
	// Default skips link members entirely.
	RecreateLinks bool
	// MaxFileBytes caps one member's expanded size; MaxTotalBytes caps
	// the whole archive. Zero means unlimited.
	MaxFileBytes  int64
	MaxTotalBytes int64
}

// UntarGz extracts a gzipped tar stream into dest.
func UntarGz(r io.Reader, dest string, opts *Options) error {
	ex := extractor{dest: dest}
	if opts != nil {
		ex.opts = *opts
	}
	return WalkTarGz(r, func(hdr *tar.Header, body io.Reader) (bool, error) {
		if err := ex.tarEntry(hdr, body); err != nil {
			return false, err
		}
		return true, nil
	})
}

// Unzip extracts a zip archive into dest.
func Unzip(zr *zip.Reader, dest string, opts *Options) error {
	ex := extractor{dest: dest}
	if opts != nil {
		ex.opts = *opts
	}
	return WalkZip(zr, func(f *zip.File) (bool, error) {
		if err := ex.zipEntry(f); err != nil {
			return false, err
		}
		return true, nil
	})
}

// UnzipAt extracts a zip archive read from ra (e.g. a downloaded file).
func UnzipAt(ra io.ReaderAt, size int64, dest string, opts *Options) error {
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	return Unzip(zr, dest, opts)
}

// WalkTarGz calls fn for each entry of a gzipped tar stream; fn may read
// the entry payload from body. Returning false stops the walk early.
func WalkTarGz(r io.Reader, fn func(hdr *tar.Header, body io.Reader) (bool, error)) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("bad archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("bad archive: %w", err)
		}
		cont, err := fn(hdr, tr)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
	}
}

// WalkZip calls fn for each member of a zip archive; fn opens and reads
// the member itself. Returning false stops the walk early.
func WalkZip(zr *zip.Reader, fn func(f *zip.File) (bool, error)) error {
	for _, f := range zr.File {
		cont, err := fn(f)
		if err != nil {
			return err
		}
		if !cont {
			return nil
		}
	}
	return nil
}

type extractor struct {
	dest  string
	opts  Options
	total int64
}

// resolve maps a member name to its on-disk path under dest, enforcing
// the prefix filter and the traversal guard. ok=false means the entry is
// filtered out by Prefix; an error means the entry is unsafe.
func (ex *extractor) resolve(name string) (target string, ok bool, err error) {
	native := filepath.FromSlash(name)
	if native == "" || filepath.IsAbs(native) || isRootedEntry(name) || hasDotDotSegment(native) {
		return "", false, fmt.Errorf("unsafe archive entry %q", name)
	}
	clean := filepath.Clean(native)
	if ex.opts.Prefix != "" {
		prefix := filepath.FromSlash(strings.Trim(ex.opts.Prefix, "/")) + string(filepath.Separator)
		if !strings.HasPrefix(clean, prefix) {
			return "", false, nil
		}
		clean = strings.TrimPrefix(clean, prefix)
	}
	target = filepath.Join(ex.dest, clean)
	if rel, rerr := filepath.Rel(ex.dest, target); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("unsafe archive entry %q", name)
	}
	return target, true, nil
}

// isRootedEntry reports whether an archive entry name starts at a filesystem
// root, in either slash direction. filepath.IsAbs is not enough on its own:
// on Windows a name like `\evil` is rooted but not absolute because it has no
// volume name, so it would otherwise slip through and be silently relocated
// under the destination instead of being rejected.
func isRootedEntry(name string) bool {
	return strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\")
}

// hasDotDotSegment reports whether name contains a ".." path segment,
// splitting on both separators so Windows-style "..\" escapes are caught
// on every platform.
func hasDotDotSegment(name string) bool {
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return true
		}
	}
	return false
}

func (ex *extractor) tarEntry(hdr *tar.Header, body io.Reader) error {
	target, ok, err := ex.resolve(hdr.Name)
	if err != nil || !ok {
		return err
	}
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg, tar.TypeRegA:
		return ex.writeFile(target, os.FileMode(hdr.Mode).Perm(), body)
	case tar.TypeSymlink, tar.TypeLink:
		if !ex.opts.RecreateLinks {
			return nil
		}
		return ex.linkEntry(hdr, target)
	default:
		// Device nodes, fifos, and other exotic entries are skipped.
		return nil
	}
}

func (ex *extractor) zipEntry(f *zip.File) error {
	target, ok, err := ex.resolve(f.Name)
	if err != nil || !ok {
		return err
	}
	if f.FileInfo().IsDir() {
		mode := f.Mode()
		if mode.Perm() == 0 || ex.opts.FileMode != 0 {
			mode = 0o755
		}
		return os.MkdirAll(target, mode)
	}
	if f.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	return ex.writeFile(target, f.Mode(), rc)
}

func (ex *extractor) writeFile(target string, entryMode os.FileMode, body io.Reader) error {
	mode := entryMode
	if ex.opts.FileMode != 0 {
		mode = ex.opts.FileMode
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	src := body
	if ex.opts.MaxFileBytes > 0 {
		src = &io.LimitedReader{R: body, N: ex.opts.MaxFileBytes + 1}
	}
	n, err := io.Copy(out, src)
	cerr := out.Close()
	if err != nil {
		return err
	}
	if cerr != nil {
		return cerr
	}
	if ex.opts.MaxFileBytes > 0 && n > ex.opts.MaxFileBytes {
		_ = os.Remove(target)
		return fmt.Errorf("archive: member expands past %d byte limit", ex.opts.MaxFileBytes)
	}
	ex.total += n
	if ex.opts.MaxTotalBytes > 0 && ex.total > ex.opts.MaxTotalBytes {
		_ = os.Remove(target)
		return fmt.Errorf("archive: contents expand past %d byte total limit", ex.opts.MaxTotalBytes)
	}
	return nil
}

// linkEntry recreates a tar symlink or hardlink at target, rejecting link
// targets that resolve outside dest.
func (ex *extractor) linkEntry(hdr *tar.Header, target string) error {
	linkname := filepath.FromSlash(hdr.Linkname)
	if filepath.IsAbs(linkname) || hasDotDotSegment(linkname) {
		return fmt.Errorf("unsafe archive link target %q", hdr.Linkname)
	}
	if hdr.Typeflag == tar.TypeSymlink {
		// The link target resolves relative to the link's directory and
		// must land inside dest.
		resolved := filepath.Join(filepath.Dir(target), linkname)
		if rel, rerr := filepath.Rel(ex.dest, resolved); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive link target %q", hdr.Linkname)
		}
		_ = os.Remove(target)
		return os.Symlink(linkname, target)
	}
	// Hardlink names are archive-root-relative; under a Prefix scope they
	// are relative to the stripped root.
	srcName := linkname
	if ex.opts.Prefix != "" {
		srcName = filepath.Join(filepath.FromSlash(strings.Trim(ex.opts.Prefix, "/")), linkname)
	}
	src, ok, err := ex.resolve(srcName)
	if err != nil || !ok {
		return fmt.Errorf("unsafe archive link target %q", hdr.Linkname)
	}
	_ = os.Remove(target)
	return os.Link(src, target)
}
