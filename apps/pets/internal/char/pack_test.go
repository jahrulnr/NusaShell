package char

import (
	"archive/zip"
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makeGIF builds an n-frame animated GIF of size w x h with the given delays
// (hundredths of a second).
func makeGIF(t *testing.T, w, h, n int, delays []int) []byte {
	t.Helper()
	g := &gif.GIF{
		Image: make([]*image.Paletted, n),
		Delay: make([]int, n),
	}
	palette := color.Palette{color.Transparent, color.Black}
	for i := 0; i < n; i++ {
		p := image.NewPaletted(image.Rect(0, 0, w, h), palette)
		// paint a distinct opaque pixel so frames differ
		p.SetColorIndex(i%w, i%h, 1)
		g.Image[i] = p
		g.Delay[i] = 10
		if i < len(delays) {
			g.Delay[i] = delays[i]
		}
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("encode gif: %v", err)
	}
	return buf.Bytes()
}

func writeCharFolder(t *testing.T, dir string, cfg string, files map[string][]byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDecodeGIF(t *testing.T) {
	t.Parallel()
	data := makeGIF(t, 8, 8, 3, []int{10, 20, 30})
	frames, delays, err := DecodeGIF(data)
	if err != nil {
		t.Fatalf("DecodeGIF: %v", err)
	}
	if len(frames) != 3 || len(delays) != 3 {
		t.Fatalf("got %d frames/%d delays, want 3/3", len(frames), len(delays))
	}
	// delays are hundredths -> ms: 10->100, 20->200, 30->300
	want := []int{100, 200, 300}
	for i, d := range delays {
		if d != want[i] {
			t.Fatalf("delay[%d] = %d, want %d", i, d, want[i])
		}
	}
	b := frames[0].Bounds()
	if b.Dx() != 8 || b.Dy() != 8 {
		t.Fatalf("frame size = %dx%d, want 8x8", b.Dx(), b.Dy())
	}
}

func TestDecodeGIFSingleFrame(t *testing.T) {
	t.Parallel()
	data := makeGIF(t, 4, 4, 1, nil)
	frames, _, err := DecodeGIF(data)
	if err != nil {
		t.Fatalf("DecodeGIF: %v", err)
	}
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
}

func TestDecodeGIFInvalid(t *testing.T) {
	t.Parallel()
	if _, _, err := DecodeGIF([]byte("not a gif")); err == nil {
		t.Fatal("expected error for invalid gif")
	}
}

func TestFitScale(t *testing.T) {
	t.Parallel()
	cases := []struct {
		nw, nh, mw, mh int
		want           float64
	}{
		{400, 800, 200, 400, 0.5},  // shrink by height
		{100, 100, 200, 400, 1.0},  // no enlarge
		{800, 100, 200, 400, 0.25}, // shrink by width
		{0, 0, 200, 400, 1.0},      // degenerate
	}
	for _, c := range cases {
		if got := fitScale(c.nw, c.nh, c.mw, c.mh); got != c.want {
			t.Fatalf("fitScale(%d,%d,%d,%d) = %v, want %v", c.nw, c.nh, c.mw, c.mh, got, c.want)
		}
	}
}

func TestLoadPackFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := `{
		"name": "testpet",
		"max_width": 100,
		"max_height": 100,
		"states": {
			"idle": {"gif": "idle.gif", "every": [25, 60]},
			"error": {"gif": "error.gif", "every": [0, 0]}
		}
	}`
	// 200x200 GIF -> scale 0.5 -> 100x100
	files := map[string][]byte{
		"idle.gif":  makeGIF(t, 200, 200, 2, nil),
		"error.gif": makeGIF(t, 200, 200, 1, nil),
	}
	writeCharFolder(t, dir, cfg, files)

	pack, err := LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	if pack.Name != "testpet" {
		t.Fatalf("name = %q, want testpet", pack.Name)
	}
	if pack.Scale != 0.5 {
		t.Fatalf("scale = %v, want 0.5", pack.Scale)
	}
	idle := pack.States["idle"]
	if idle == nil || len(idle.Frames) != 2 {
		t.Fatalf("idle: got %+v", idle)
	}
	b := idle.Frames[0].Bounds()
	if b.Dx() != 100 || b.Dy() != 100 {
		t.Fatalf("scaled frame = %dx%d, want 100x100", b.Dx(), b.Dy())
	}
	if idle.Every != [2]float64{25, 60} {
		t.Fatalf("every = %v, want [25 60]", idle.Every)
	}
	if pack.States["error"] == nil {
		t.Fatal("error state missing")
	}
}

func TestLoadPackMissingStateGIFSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := `{"name":"p","states":{"idle":{"gif":"idle.gif"},"gone":{"gif":"nope.gif"}}}`
	writeCharFolder(t, dir, cfg, map[string][]byte{"idle.gif": makeGIF(t, 10, 10, 1, nil)})
	pack, err := LoadPack(dir)
	if err != nil {
		t.Fatalf("LoadPack: %v", err)
	}
	if pack.States["idle"] == nil {
		t.Fatal("idle missing")
	}
	if pack.States["gone"] != nil {
		t.Fatal("gone should be skipped, not error")
	}
}

func TestLoadPackMissingConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if _, err := LoadPack(dir); err == nil {
		t.Fatal("expected error for missing config.json")
	}
}

func TestLoadPackFromZip(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	add("config.json", []byte(`{"name":"zpet","max_width":50,"max_height":50,"states":{"idle":{"gif":"idle.gif"}}}`))
	add("idle.gif", makeGIF(t, 100, 100, 2, nil))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	pack, err := LoadPackFromReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("LoadPackFromReader: %v", err)
	}
	if pack.Name != "zpet" {
		t.Fatalf("name = %q", pack.Name)
	}
	if pack.Scale != 0.5 {
		t.Fatalf("scale = %v, want 0.5", pack.Scale)
	}
	if pack.States["idle"] == nil || len(pack.States["idle"].Frames) != 2 {
		t.Fatalf("idle frames: %+v", pack.States["idle"])
	}
}

func TestSourceFolderAndZip(t *testing.T) {
	t.Parallel()
	// folder
	dir := t.TempDir()
	writeCharFolder(t, dir, `{"states":{}}`, nil)
	src, err := OpenSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if !src.IsFolder() {
		t.Fatal("expected folder source")
	}
	if !src.Has("config.json") {
		t.Fatal("folder Has config.json = false")
	}
	names := src.Names()
	found := false
	for _, n := range names {
		if n == "config.json" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Names missing config.json: %v", names)
	}
	if _, err := src.Read("config.json"); err != nil {
		t.Fatalf("Read: %v", err)
	}
}

func TestLoadStaticImageBuildsIdlePackAndPreservesAlpha(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.png")
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.SetRGBA(1, 0, color.RGBA{R: 20, G: 40, B: 60, A: 128})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	pack, err := LoadStaticImage(path, "robot", 20, 20)
	if err != nil {
		t.Fatalf("LoadStaticImage: %v", err)
	}
	if pack.Name != "robot" || pack.Scale != 1 {
		t.Fatalf("pack identity = name %q scale %v", pack.Name, pack.Scale)
	}
	anim := pack.States["idle"]
	if anim == nil || len(anim.Frames) != 1 {
		t.Fatalf("idle animation = %+v", anim)
	}
	for _, name := range []string{"thinking", "reasoning", "done", "error", "waiting"} {
		if state := pack.States[name]; state == nil || len(state.Frames) != 1 {
			t.Fatalf("static state %q = %+v, want one placeholder frame", name, state)
		}
	}
	if got := anim.Frames[0].Bounds(); got != image.Rect(0, 0, 4, 2) {
		t.Fatalf("frame bounds = %v, want (0,0)-(4,2)", got)
	}
	_, _, _, alpha := anim.Frames[0].At(1, 0).RGBA()
	if got := uint8(alpha >> 8); got != 128 {
		t.Fatalf("alpha = %d, want 128", got)
	}
}

func TestLoadStaticImageScalesToMaxBox(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "pet.png")
	img := image.NewRGBA(image.Rect(0, 0, 400, 800))
	img.SetRGBA(0, 0, color.RGBA{A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	pack, err := LoadStaticImage(path, "pet", 200, 400)
	if err != nil {
		t.Fatalf("LoadStaticImage: %v", err)
	}
	if pack.Scale != 0.5 {
		t.Fatalf("scale = %v, want 0.5", pack.Scale)
	}
	if got := pack.States["idle"].Frames[0].Bounds(); got.Dx() != 200 || got.Dy() != 400 {
		t.Fatalf("scaled frame = %v, want 200x400", got)
	}
}

func TestLoadStaticImageRejectsInvalidFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-an-image")
	if err := os.WriteFile(path, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStaticImage(path, "pet", 20, 20); err == nil {
		t.Fatal("invalid image should fail")
	}
}
