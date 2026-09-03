package bubble

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBubbleLoweredAndExactlyTwoLines(t *testing.T) {
	l := Compute(192, "Thinking…", "I think this needs a closer look at the files", Fonts{fakeFace{size: 6}, fakeFace{size: 6}})
	if l.Bg.Y < 30 || l.Caret().Y+l.Caret().H != AreaHeight-8 {
		t.Fatalf("bubble must sit near the pet: %+v caret %+v", l.Bg, l.Caret())
	}
	if len(l.Body) != 1 || !strings.HasSuffix(l.Body[0].Text, "…") {
		t.Fatalf("want one ellipsized detail line: %+v", l.Body)
	}
	if l.Header.X != l.Body[0].X {
		t.Fatalf("both text rows should share the same left edge: %+v", l)
	}
}

func TestLayoutUsesSeparateHeaderAndDetailMetrics(t *testing.T) {
	fonts := Fonts{Header: fakeFace{size: 14}, Body: fakeFace{size: 12}}
	l := Compute(192, "Thinking…", "Read the next file", fonts)
	if l.Header.H != 14 || l.Body[0].H != 12 || l.Header.X != l.Body[0].X {
		t.Fatalf("want larger header and aligned detail: %+v", l)
	}
	if l.Body[0].Y < l.Header.Y+l.Header.H+4 {
		t.Fatal("text rows overlap")
	}
}

func TestBubbleFitsNarrowCanvasAndLongTokens(t *testing.T) {
	for _, width := range []int{64, 128, 192} {
		l := Compute(width, strings.Repeat("Thinking", 10), "read_file(\""+strings.Repeat("界", 100)+"\")\nnext line", Fonts{fakeFace{size: 6}, fakeFace{size: 6}})
		if len(l.Body) != 1 {
			t.Fatalf("width %d: want exactly one body line", width)
		}
		for _, line := range []Line{l.Header, l.Body[0]} {
			w, _ := (fakeFace{size: 6}).Measure(line.Text)
			if !utf8.ValidString(line.Text) || strings.Contains(line.Text, "\n") || w > line.W || line.X+line.W > width {
				t.Fatalf("width %d: text escapes or splits UTF-8: %+v", width, line)
			}
		}
	}
}
