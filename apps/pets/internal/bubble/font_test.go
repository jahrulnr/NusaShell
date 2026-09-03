package bubble

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
)

func TestLoadFontsUsesBoldHeaderAndSmallerRegularDetail(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "Example-Regular.ttf")
	for path, data := range map[string][]byte{regular: goregular.TTF, filepath.Join(dir, "Example-Bold.ttf"): gobold.TTF} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fonts, err := LoadFonts(regular)
	if err != nil {
		t.Fatal(err)
	}
	_, headerH := fonts.Header.Measure("Thinking…")
	_, bodyH := fonts.Body.Measure("Thinking…")
	if HeaderSize != 14 || BodySize != 12 || headerH <= bodyH {
		t.Fatalf("expected 14px header, 12px body: sizes=%d/%d heights=%d/%d", HeaderSize, BodySize, headerH, bodyH)
	}
	bold, err := LoadFace(filepath.Join(dir, "Example-Bold.ttf"), 14)
	if err != nil {
		t.Fatal(err)
	}
	wantW, _ := bold.Measure("Thinking…")
	if gotW, _ := fonts.Header.Measure("Thinking…"); gotW != wantW {
		t.Fatal("header did not select its real bold sibling")
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 192, 32))
	fonts.Body.Draw(canvas, 0, 0, "Reading files…", color.White)
	partial := 0
	for i := 3; i < len(canvas.Pix); i += 4 {
		if canvas.Pix[i] > 0 && canvas.Pix[i] < 255 {
			partial++
		}
	}
	if partial < 20 {
		t.Fatalf("font lacks antialiased strokes: %d coverage pixels", partial)
	}
}
