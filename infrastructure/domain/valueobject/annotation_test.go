package valueobject

import (
	"strings"
	"testing"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

func TestAnnotationNote(t *testing.T) {
	if AnnotationNote(nil) != "" {
		t.Fatal("no annotations must render nothing")
	}
	element := Annotation{Marker: 1, Comment: " make it bigger ", Screenshot: true, Descriptor: AnnotationDescriptor{
		Kind: enum.AnnotationElement, TagName: "button", Name: "Sign in", Selector: "form > button`x",
		PageURL: "http://127.0.0.1/", Rect: AnnotationRect{X: 10, Y: 20, Width: 50, Height: 20},
		Point: AnnotationPoint{X: 30, Y: 25}, Viewport: AnnotationViewport{Width: 1280, Height: 800, DPR: 1},
		Style: AnnotationStyle{FontSize: "13px", Display: "inline-block", Position: "static"},
	}}
	region := Annotation{Marker: 1, Comment: "empty", Descriptor: AnnotationDescriptor{
		Kind: enum.AnnotationRegion, Title: "Login", PageURL: "http://127.0.0.1/",
		Rect: AnnotationRect{X: 5, Y: 6, Width: 400, Height: 200},
	}}
	page := Annotation{Screenshot: true, Descriptor: AnnotationDescriptor{Kind: enum.AnnotationPage, Title: "Login"}}
	note := AnnotationNote([]Annotation{element, region, page})
	for _, want := range []string{
		"<annotation-context>",
		"[1] <button> \"Sign in\" — selector `form > button'x` — http://127.0.0.1/\n    Comment: make it bigger",
		"Layout: 10,20 50×20 px · picked at 30,25 · viewport 1280×800 @1x",
		"Style: font 13px · inline-block/static",
		// A repeated marker takes the next free number.
		"[2] region 400×200 px at 5,6 \"Login\" — http://127.0.0.1/ (no screenshot)\n    Comment: empty",
		"[3] page \"Login\"",
		"</annotation-context>",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("note lacks %q:\n%s", want, note)
		}
	}
}

func TestAnnotationLabel(t *testing.T) {
	cases := map[string]AnnotationDescriptor{
		"Sign in":        {Kind: enum.AnnotationElement, Name: "Sign in", ImmediateText: "x"},
		"hello":          {Kind: enum.AnnotationElement, ImmediateText: "hello"},
		"region 400×200": {Kind: enum.AnnotationRegion, Rect: AnnotationRect{Width: 400, Height: 200}},
		"page":           {Kind: enum.AnnotationPage},
	}
	for want, d := range cases {
		if got := d.Label(); got != want {
			t.Errorf("Label() = %q, want %q", got, want)
		}
	}
}
