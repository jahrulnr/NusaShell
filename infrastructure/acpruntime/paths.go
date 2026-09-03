package acpruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"nusashell/domain"
)

// resolveAcpWorkspace normalizes local workspace aliases before they become
// the ACP cwd, connection-pool key, and permission-policy root. Remote ACP
// agents own their filesystem, so their workspace is an opaque remote path.
func resolveAcpWorkspace(agent *domain.AcpAgent, requested string) (string, error) {
	workspace := strings.TrimSpace(requested)
	if workspace == "" && agent != nil {
		workspace = strings.TrimSpace(agent.DefaultWorkspace)
	}
	if workspace == "" {
		workspace, _ = os.Getwd()
	}
	if agent != nil && agent.EffectiveTransport() == domain.AcpTransportRemote {
		return filepath.Clean(workspace), nil
	}
	return canonicalWorkspace(workspace)
}

func canonicalWorkspace(workspace string) (string, error) {
	canonical, err := canonicalPath(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace %q: %w", workspace, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat workspace %q: %w", workspace, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", workspace)
	}
	return canonical, nil
}

// canonicalPath resolves all existing symlink components and preserves a
// missing suffix. ACP write tools commonly validate a path before the file
// exists, so resolving only an already-existing target is insufficient.
func canonicalPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	clean := filepath.Clean(path)
	abs := clean
	if !domain.PathRooted(clean) {
		var err error
		abs, err = filepath.Abs(clean)
		if err != nil {
			return "", err
		}
	}

	probe := abs
	missing := make([]string, 0, 4)
	for {
		resolved, evalErr := filepath.EvalSymlinks(probe)
		if evalErr == nil {
			resolved = filepath.Clean(resolved)
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(evalErr) {
			return "", evalErr
		}
		// EvalSymlinks returns ENOENT for a dangling symlink even though
		// the symlink itself exists. Resolve that link before walking to
		// its parent, otherwise a write through a dangling link could be
		// misclassified as an in-workspace missing suffix.
		if info, lstatErr := os.Lstat(probe); lstatErr == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return "", evalErr
			}
			link, readlinkErr := os.Readlink(probe)
			if readlinkErr != nil {
				return "", readlinkErr
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(probe), link)
			}
			probe = filepath.Clean(link)
			continue
		} else if !os.IsNotExist(lstatErr) {
			return "", lstatErr
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", evalErr
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func containedPath(workspace, path string) (string, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(path)
	if target == "" {
		return "", fmt.Errorf("path is empty")
	}
	if !domain.PathRooted(target) {
		target = filepath.Join(root, target)
	}
	canonical, err := canonicalPath(target)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	clean, ok := domain.ResolveWithinWorkspace(root, canonical)
	if !ok {
		return "", fmt.Errorf("path %q is outside the bound workspace", path)
	}
	return clean, nil
}
