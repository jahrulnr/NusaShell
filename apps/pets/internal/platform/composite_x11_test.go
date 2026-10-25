//go:build sdl2

package platform

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/veandco/go-sdl2/sdl"
	"nusashell-pets/internal/app"
)

// Run in an isolated server: PETS_X11_INTEGRATION=1 xvfb-run -a make test-sdl2.
// Never mutate the human's desktop as a side effect of ordinary unit tests.
func TestAutomaticCompositePreservesOtherClients(t *testing.T) {
	if os.Getenv("PETS_X11_INTEGRATION") != "1" {
		t.Skip("requires isolated X11 server; see test comment")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := sdl.Init(sdl.INIT_VIDEO); err != nil {
		t.Fatal(err)
	}
	defer sdl.Quit()
	w, err := app.NewWindow(app.WindowConfig{Width: 16, Height: 16}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Destroy()
	display, _, err := w.X11Handles()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PETS_COMPOSITE_EXIT_CHILD") == "1" {
		if err := EnableAutomaticCompositing(display); err != nil {
			t.Fatal(err)
		}
		println("composite-ready")
		select {}
	}
	probe := filepath.Join(t.TempDir(), "composite-probe")
	flags, err := exec.Command("pkg-config", "--cflags", "--libs", "x11", "xext", "xcomposite").Output()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"testdata/composite_probe.c", "-o", probe}, strings.Fields(string(flags))...)
	if out, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, out)
	}
	// Reuse the native compiler/display harness to validate activation packets,
	// including minimized, unrelated, popup, and already-destroyed client IDs.
	focusProbe := filepath.Join(t.TempDir(), "focus-probe")
	args = append([]string{"testdata/focus_probe.c", "-o", focusProbe}, strings.Fields(string(flags))...)
	if out, err := exec.Command("cc", args...).CombinedOutput(); err != nil {
		t.Fatalf("compile focus: %v\n%s", err, out)
	}
	for _, mode := range []string{"1", "0"} {
		child := exec.Command(focusProbe, mode)
		input, err := child.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		output, err := child.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(output)
		if !scanner.Scan() || scanner.Text() != "ready" {
			_ = child.Process.Kill()
			_ = child.Wait()
			t.Fatal("focus fixture startup failed")
		}
		got := FocusElectronWindow(display)
		_, _ = input.Write([]byte("done\n"))
		_ = input.Close()
		for scanner.Scan() {
			t.Log(scanner.Text())
		}
		if err := child.Wait(); err != nil {
			t.Fatalf("focus protocol: %v", err)
		}
		if got != (mode == "1") {
			t.Fatalf("activation request = %v for mode %s", got, mode)
		}
	}
	if err := EnableAutomaticCompositing(display); err != nil {
		t.Fatal(err)
	}
	active := true
	defer func() {
		if active {
			DisableAutomaticCompositing(display)
		}
	}()
	for i := 0; i < 3; i++ {
		if out, err := exec.Command(probe, "1").CombinedOutput(); err != nil {
			t.Fatalf("retained pixels and clear: %v\n%s", err, out)
		} else {
			t.Logf("%s", out)
		}
	}
	DisableAutomaticCompositing(display)
	active = false
	if out, err := exec.Command(probe, "0").CombinedOutput(); err != nil {
		t.Fatalf("cleanup: %v\n%s", err, out)
	} else {
		t.Logf("%s", out)
	}
	// A hard process death must release the server-side request without defer.
	child := exec.Command(os.Args[0], "-test.run=^TestAutomaticCompositePreservesOtherClients$")
	child.Env = append(os.Environ(), "PETS_COMPOSITE_EXIT_CHILD=1")
	ready, err := child.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	}()
	lines := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(ready)
		for scanner.Scan() {
			if scanner.Text() == "composite-ready" {
				lines <- true
				return
			}
		}
		lines <- false
	}()
	select {
	case ok := <-lines:
		if !ok {
			t.Fatal("child did not enable compositing")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("child startup timed out")
	}
	if out, err := exec.Command(probe, "1").CombinedOutput(); err != nil {
		t.Fatalf("child redirect: %v\n%s", err, out)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	if out, err := exec.Command(probe, "0").CombinedOutput(); err != nil {
		t.Fatalf("disconnect cleanup: %v\n%s", err, out)
	}

}
