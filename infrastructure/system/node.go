package system

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// NodeDir is the directory holding a Node binary an agent launched from a
// script may use: the one on PATH, else one of nvm's. Many agent adapters
// are scripts that start with "#!/usr/bin/env node", and a server started
// by a service manager has a PATH that lacks the shell's nvm setup. Empty
// means none was found.
func NodeDir() string {
	if p, err := exec.LookPath("node"); err == nil {
		return filepath.Dir(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return nodeDirIn(home)
}

// nodeDirIn looks for nvm's default Node under home, then its newest.
func nodeDirIn(home string) string {
	root := filepath.Join(home, ".nvm", "versions", "node")
	if b, err := os.ReadFile(filepath.Join(home, ".nvm", "alias", "default")); err == nil {
		if dir := nodeMatching(root, strings.TrimSpace(string(b))); dir != "" {
			return dir
		}
	}
	return newestNode(root, "")
}

// nodeMatching resolves an nvm alias, which may name a whole version
// ("v26.12.1") or a prefix of one ("26").
func nodeMatching(root, version string) string {
	if version == "" {
		return ""
	}
	if dir := nodeBinDir(filepath.Join(root, version)); dir != "" {
		return dir
	}
	if !strings.HasPrefix(version, "v") {
		version = "v" + version
	}
	return newestNode(root, version)
}

// newestNode is the highest installed version whose name starts with prefix.
func newestNode(root, prefix string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return versionLess(names[i], names[j]) })
	for _, name := range slices.Backward(names) {
		if dir := nodeBinDir(filepath.Join(root, name)); dir != "" {
			return dir
		}
	}
	return ""
}

// nodeBinDir is versionDir's bin directory when it holds a node binary.
func nodeBinDir(versionDir string) string {
	bin := filepath.Join(versionDir, "bin")
	if st, err := os.Stat(filepath.Join(bin, "node")); err != nil || st.IsDir() {
		return ""
	}
	return bin
}

// versionLess orders dotted versions numerically, so v9 sorts below v10.
func versionLess(a, b string) bool {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

func versionParts(v string) []int {
	var out []int
	for part := range strings.SplitSeq(strings.TrimPrefix(v, "v"), ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			break
		}
		out = append(out, n)
	}
	return out
}
