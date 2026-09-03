package bubble

import (
	"image"
	"image/color"
	"os"
	"testing"
)

// fakeFace measures one pixel-column per rune and paints solid squares, so
// geometry and pixel tests stay deterministic without a real font file.
type fakeFace struct{ size int }

func TestDefaultStyleUsesDarkPanelAndLightText(t *testing.T) {
	style := DefaultStyle()
	bg := color.RGBAModel.Convert(style.Bg).(color.RGBA)
	if max(bg.R, bg.G, bg.B) > 64 || bg.A != 255 {
		t.Fatalf("bubble must have an opaque dark background, got %v", bg)
	}
	for name, c := range map[string]color.Color{"header": style.HeaderText, "body": style.BodyText} {
		text := color.RGBAModel.Convert(c).(color.RGBA)
		if min(text.R, text.G, text.B) < 180 || text.A != 255 {
			t.Errorf("%s must use light text, got %v", name, text)
		}
	}
}

func (f fakeFace) Measure(text string) (int, int) { return len(text) * f.size, f.size }

func (f fakeFace) Draw(dst *image.RGBA, x, y int, text string, c color.Color) {
	for i := 0; i < len(text); i++ {
		for yy := 0; yy < f.size; yy++ {
			for xx := 0; xx < f.size; xx++ {
				dst.Set(x+i*f.size+xx, y+yy, c)
			}
		}
	}
}

func TestEllipsizeFitsWordsAndUnbrokenTokens(t *testing.T) {
	t.Parallel()
	f := fakeFace{size: 6}
	for _, text := range []string{"alpha beta gamma", "verylongwordwithoutspaces", "界界界界界界", "  hello\n world  "} {
		got := ellipsize(text, 60, f)
		if w, _ := f.Measure(got); w > 60 {
			t.Fatalf("%q measures %d, exceeds 60", got, w)
		}
	}
	if got := ellipsize("", 60, f); got != "" {
		t.Fatalf("empty text = %q", got)
	}
	if got := ellipsize("hello", 60, f); got != "hello" {
		t.Fatalf("short text = %q", got)
	}
	if got := ellipsize("overflow", 1, f); got != "" {
		t.Fatalf("narrow text = %q", got)
	}
}

func TestLayoutGeometryAndStableSize(t *testing.T) {
	t.Parallel()
	f := fakeFace{size: 10}
	l := Compute(192, "Thinking", "memory saved now write the answer", Fonts{f, f})
	short := Compute(192, "Done", "All set", Fonts{f, f})
	if l.Bg != short.Bg || l.Bg.X <= 0 || l.Bg.W > MaxWidth {
		t.Fatalf("unstable or overflowing panel: %+v / %+v", l.Bg, short.Bg)
	}
	if len(l.Body) != 1 || l.CaretX != l.Bg.X+l.Bg.W/2 {
		t.Fatalf("layout = %+v", l)
	}
	last := l.Body[0]
	if last.Y+last.H > l.Bg.Y+l.Bg.H-padY {
		t.Fatalf("body %+v escapes panel %+v", last, l.Bg)
	}
	if l.Caret().Y+l.Caret().H > AreaHeight-2 {
		t.Fatalf("caret escapes reserved strip: %+v", l.Caret())
	}
}

func TestDrawPaintsExpectedPixels(t *testing.T) {
	t.Parallel()
	f := fakeFace{size: 6}
	l := Compute(192, "Thinking", "memory saved", Fonts{f, f})
	canvas := image.NewRGBA(image.Rect(0, 0, 192, AreaHeight))
	style := DefaultStyle()
	Draw(canvas, l, style, Fonts{f, f})
	checks := []struct {
		x, y int
		want color.Color
	}{
		{l.Bg.X + l.Bg.W/2, l.Bg.Y, style.Border},
		{0, 0, color.RGBA{}},
		{l.Header.X + 2, l.Header.Y + 2, style.HeaderText},
		{l.Body[0].X + 2, l.Body[0].Y + 2, style.BodyText},
	}
	for _, check := range checks {
		if got := canvas.At(check.x, check.y); got != check.want {
			t.Errorf("pixel (%d,%d) = %v, want %v", check.x, check.y, got, check.want)
		}
	}
	// The pointer narrows toward the pet, not upward as in the old bubble.
	for y := l.Caret().Y; y < l.Caret().Y+l.Caret().H; y++ {
		half := (l.Caret().Y + l.Caret().H - y) * l.Caret().W / (2 * l.Caret().H)
		if got := canvas.RGBAAt(l.CaretX+half+1, y); got.A != 0 {
			t.Fatalf("caret widens outside its downward triangle at y=%d", y)
		}
	}
}

func TestFindFontPrefersConfiguredThenSystem(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ok := dir + "/mine.ttf"
	if err := writeFile(ok, []byte("font")); err != nil {
		t.Fatal(err)
	}
	if got := FindFont(ok); got != ok {
		t.Fatalf("configured font = %q, want %q", got, ok)
	}
	sys := dir + "/system.ttf"
	if err := writeFile(sys, []byte("font")); err != nil {
		t.Fatal(err)
	}
	if got := FindFont("", []string{dir + "/missing.ttf", sys}); got != sys {
		t.Fatalf("fallback font = %q, want %q", got, sys)
	}
	if got := FindFont("", []string{dir + "/missing.ttf"}); got != "" {
		t.Fatalf("no font = %q, want empty", got)
	}
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func TestLoadFaceRejectsMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := LoadFace(t.TempDir()+"/nope.ttf", 12); err == nil {
		t.Fatal("expected error for missing font file")
	}
}
