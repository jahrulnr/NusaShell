package symbol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	drepo "github.com/jahrulnr/nusashell/infrastructure/domain/repository"
	svc "github.com/jahrulnr/nusashell/infrastructure/domain/service"
)

func TestIsFilesystemRoot(t *testing.T) {
	roots := []string{"/", "C:\\", "D:\\", "Z:\\", "c:/", `\\server\share`, `\\server\share\`, "//nas/volume"}
	for _, dir := range roots {
		if !isFilesystemRoot(dir) {
			t.Errorf("%q should be a filesystem root", dir)
		}
	}
	non := []string{"/home/x", `C:\Users`, `C:\work\app`, `D:\projects\repo`, `\\server\share\dir`, "/tmp/x", ".", "relative/dir"}
	for _, dir := range non {
		if isFilesystemRoot(dir) {
			t.Errorf("%q is not a filesystem root", dir)
		}
	}
}

// fakeWS is a minimal repository.Workspace over a scripted entry list —
// enough to drive WorkspaceRoot.Walk's directory filtering.
type fakeWS struct{ entries []fakeEntry }
type fakeEntry struct {
	rel   string
	isDir bool
	size  int64
	body  string
}

func (w fakeWS) CheckRoot(dir string) (string, error) { return dir, nil }
func (w fakeWS) OpenHost(context.Context, string) (io.ReadCloser, fs.FileInfo, error) {
	return nil, nil, errors.New("no host access")
}
func (w fakeWS) HostURI(string) string { return "" }
func (w fakeWS) Open(context.Context, string) (drepo.WorkspaceSession, error) {
	return fakeSess{w}, nil
}

func (w fakeWS) Entries(_ context.Context, _ string, visit func(rel string, isDir bool) error) error {
	var pruned []string
	for _, e := range w.entries {
		skip := false
		for _, p := range pruned {
			if strings.HasPrefix(e.rel, p) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		err := visit(e.rel, e.isDir)
		switch err {
		case fs.SkipDir:
			pruned = append(pruned, e.rel+"/")
		case fs.SkipAll:
			return nil
		case nil:
		default:
			return err
		}
	}
	return nil
}

type fakeSess struct{ w fakeWS }

type fakeInfo struct {
	name string
	size int64
}

func (i fakeInfo) Name() string       { return i.name }
func (i fakeInfo) Size() int64        { return i.size }
func (i fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (i fakeInfo) ModTime() time.Time { return time.Time{} }
func (i fakeInfo) IsDir() bool        { return false }
func (i fakeInfo) Sys() any           { return nil }

func (s fakeSess) Stat(name string) (fs.FileInfo, error) {
	for _, e := range s.w.entries {
		if e.rel == name && !e.isDir {
			return fakeInfo{name: name, size: e.size}, nil
		}
	}
	return nil, fs.ErrNotExist
}
func (s fakeSess) ReadFile(name string) ([]byte, error) {
	for _, e := range s.w.entries {
		if e.rel == name && !e.isDir {
			return []byte(e.body), nil
		}
	}
	return nil, fs.ErrNotExist
}
func (s fakeSess) FS() fs.FS                                 { return nil }
func (s fakeSess) CreateFile(string) (io.WriteCloser, error) { return nil, errors.New("ro") }
func (s fakeSess) OpenRegular(string) (io.ReadCloser, fs.FileInfo, error) {
	return nil, nil, fs.ErrNotExist
}
func (s fakeSess) WriteFile(string, []byte, fs.FileMode) error { return errors.New("ro") }
func (s fakeSess) MkdirAll(string, fs.FileMode) error          { return errors.New("ro") }
func (s fakeSess) AttachmentURI(name string) string            { return "file://" + name }
func (s fakeSess) Close() error                                { return nil }

var _ drepo.Workspace = fakeWS{}

// TestFsRootSkipsOSDirs: at a filesystem root the OS directories are
// pruned inside Walk; at a normal project root the same names survive.
func TestFsRootSkipsOSDirs(t *testing.T) {
	entries := []fakeEntry{
		{rel: "proc", isDir: true},
		{rel: "proc/cpuinfo", size: 10},
		{rel: "usr", isDir: true},
		{rel: "usr/bin/tool", size: 10},
		{rel: "Windows", isDir: true},
		{rel: "Windows/system.ini", size: 10},
		{rel: "src", isDir: true},
		{rel: "src/main.go", size: 40, body: "package main"},
	}
	root, err := OpenWorkspaceRoot(context.Background(), fakeWS{entries}, "/")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	root.Walk(context.Background(), func(rel string, _ int64) error {
		got = append(got, rel)
		return nil
	})
	if len(got) != 1 || got[0] != "src/main.go" {
		t.Fatalf("fs root walk should keep only project dirs, got %v", got)
	}

	// Same tree under a project root: var/ and usr/ are just folders.
	root2, err := OpenWorkspaceRoot(context.Background(), fakeWS{entries}, "/work/app")
	if err != nil {
		t.Fatal(err)
	}
	got = got[:0]
	root2.Walk(context.Background(), func(rel string, _ int64) error {
		got = append(got, rel)
		return nil
	})
	if len(got) != 4 {
		t.Fatalf("project root keeps OS-named dirs, got %v", got)
	}
}

func TestGateSkipsUnmarkedBigRoot(t *testing.T) {
	root := &memRoot{files: map[string]string{}}
	for i := 0; i < GateFileLimit+100; i++ {
		root.files[fmt.Sprintf("src/f%04d.go", i)] = "package x\n\nfunc F() {}\n"
	}
	idx := &countingIndex{done: make(chan struct{})}
	m := testManager(idx, nil)

	out := m.Map(context.Background(), "home", "/home/user", opener(root))
	if !strings.HasPrefix(out, "symbol map skipped: /home/user is not a project root") {
		t.Fatalf("expected skip record, got:\n%s", out)
	}
	if idx.calls.Load() != 0 {
		t.Fatal("gate must skip before any index build")
	}
	if got := root.walks.Load(); got > GateFileLimit+2 {
		t.Fatalf("probe should stop at the gate limit, walked %d", got)
	}

	// Second turn: verdict is cached — no new walk at all.
	out2 := m.Map(context.Background(), "home", "/home/user", opener(root))
	if out2 != out {
		t.Fatal("cached verdict should return the identical skip line")
	}
	if got := root.walks.Load(); got > GateFileLimit+2 {
		t.Fatalf("cached verdict must not re-probe, walked %d total", got)
	}
}

func TestGateMarkerAllowsIndex(t *testing.T) {
	root := &memRoot{files: map[string]string{
		"go.mod":  "module example.com/x\n",
		"main.go": "package main\n\nfunc main() {}\n",
	}}
	idx := &countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "main.go", Lang: "go", Kind: "function", Name: "main", Line: 3}}}
	m := testManager(idx, nil)
	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, idx)
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.HasPrefix(out, "symbol map for /ws — declarations only") {
		t.Fatalf("marker root must index normally, got:\n%s", out)
	}
}

func TestFileCapFooter(t *testing.T) {
	root := &memRoot{files: map[string]string{"go.mod": "module x\n"}}
	for i := 0; i < MaxIndexedFiles+3; i++ {
		root.files[fmt.Sprintf("f%05d.go", i)] = "package x\n"
	}
	idx := &countingIndex{done: make(chan struct{})}
	m := testManager(idx, nil)
	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, idx)
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(out, "truncated: file cap") {
		t.Fatalf("expected file cap footer, got:\n%.300s", out)
	}
}

func TestDepthCapFooter(t *testing.T) {
	root := &memRoot{files: map[string]string{
		"go.mod":                        "module x\n",
		"a/b/c/d/e/f/g/h/i/j/k/deep.go": "package x\n",
		"ok.go":                         "package x\n\nfunc Ok() {}\n",
	}}
	idx := &countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "ok.go", Lang: "go", Kind: "function", Name: "Ok", Line: 3, Public: true}}}
	m := testManager(idx, nil)
	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, idx)
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(out, "1 files skipped: depth cap") {
		t.Fatalf("expected depth cap footer, got:\n%s", out)
	}
}

func TestProbeTimeCap(t *testing.T) {
	root := &memRoot{files: map[string]string{"go.mod": "module x\n", "a.go": "package a\n", "b.go": "package b\n"}, walkDelay: 100 * time.Millisecond}
	idx := &countingIndex{done: make(chan struct{})}
	m := testManager(idx, nil)
	m.probeTimeout = 50 * time.Millisecond
	out := m.Map(context.Background(), "ws", "/ws", opener(root))
	if !strings.Contains(out, "symbol map skipped:") || !strings.Contains(out, "walk limit") {
		t.Fatalf("expected time-cap skip, got:\n%s", out)
	}
	if idx.calls.Load() != 0 {
		t.Fatal("time cap must skip before any index build")
	}
}

// slowIndex blocks inside Index past the manager's index timeout.
type slowIndex struct {
	countingIndex
	delay time.Duration
}

func (i *slowIndex) Index(ctx context.Context, _ svc.SymbolRoot) ([]svc.Symbol, error) {
	i.calls.Add(1)
	select {
	case <-i.done:
	default:
		close(i.done)
	}
	select {
	case <-ctx.Done():
		return i.syms, ctx.Err()
	case <-time.After(i.delay):
		return i.syms, nil
	}
}

func TestIndexTimeCapKeepsPartial(t *testing.T) {
	root := &memRoot{files: map[string]string{"go.mod": "module x\n", "a.go": "package a\n\nfunc A() {}\n"}}
	idx := &slowIndex{countingIndex: countingIndex{done: make(chan struct{}), syms: []svc.Symbol{{Path: "a.go", Lang: "go", Kind: "function", Name: "A", Line: 3}}}, delay: time.Second}
	m := testManager(idx, nil)
	m.indexTimeout = 50 * time.Millisecond
	m.Map(context.Background(), "ws", "/ws", opener(root))
	waitBuild(t, &idx.countingIndex)
	deadline := time.Now().Add(3 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out = m.Map(context.Background(), "ws", "/ws", opener(root))
		if !strings.Contains(out, "is being built") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(out, "truncated: time cap") {
		t.Fatalf("expected time-cap footer on partial map, got:\n%s", out)
	}
	if !strings.Contains(out, "a.go") {
		t.Fatalf("partial symbols should still render, got:\n%s", out)
	}
}

func TestSkipVerdictExpires(t *testing.T) {
	root := &memRoot{files: map[string]string{}}
	for i := 0; i < GateFileLimit+10; i++ {
		root.files[fmt.Sprintf("f%04d.go", i)] = "package x\n"
	}
	idx := &countingIndex{done: make(chan struct{})}
	m := testManager(idx, nil)
	m.skipTTL = 30 * time.Millisecond
	m.Map(context.Background(), "ws", "/ws", opener(root))
	walksAfter := root.walks.Load()
	time.Sleep(40 * time.Millisecond)
	m.Map(context.Background(), "ws", "/ws", opener(root))
	if root.walks.Load() <= walksAfter {
		t.Fatal("expired verdict should re-probe")
	}
}
