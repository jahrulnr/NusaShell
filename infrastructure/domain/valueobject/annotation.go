package valueobject

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jahrulnr/nusashell/infrastructure/domain/enum"
)

// AnnotationPoint and AnnotationRect are viewport CSS pixels.
type AnnotationPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type AnnotationRect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// AnnotationViewport is the layout viewport when the mark was made; its
// width is the breakpoint a layout problem was seen at.
type AnnotationViewport struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	DPR    float64 `json:"dpr"`
}

// AnnotationStyle is the resolved style most often asked about when a
// layout or typography problem is diagnosed.
type AnnotationStyle struct {
	FontSize        string `json:"fontSize,omitempty"`
	LineHeight      string `json:"lineHeight,omitempty"`
	FontFamily      string `json:"fontFamily,omitempty"`
	FontWeight      string `json:"fontWeight,omitempty"`
	LetterSpacing   string `json:"letterSpacing,omitempty"`
	Color           string `json:"color,omitempty"`
	BackgroundColor string `json:"backgroundColor,omitempty"`
	Display         string `json:"display,omitempty"`
	Position        string `json:"position,omitempty"`
}

// AnnotationDescriptor is what the page said about a mark when it was
// made. Element marks carry the element fields; region and page marks
// carry only Rect (region) and the page fields.
type AnnotationDescriptor struct {
	Kind          enum.AnnotationKind `json:"kind"`
	Selector      string              `json:"selector,omitempty"`
	ElementPath   string              `json:"elementPath,omitempty"`
	TagName       string              `json:"tagName,omitempty"`
	HTML          string              `json:"html,omitempty"` // the opening tag, capped
	Role          string              `json:"role,omitempty"`
	Name          string              `json:"name,omitempty"`
	ImmediateText string              `json:"immediateText,omitempty"`
	NearbyText    string              `json:"nearbyText,omitempty"`
	Rect          AnnotationRect      `json:"rect"`
	IsFixed       bool                `json:"isFixed,omitempty"`
	FramePath     []string            `json:"framePath,omitempty"`
	Point         AnnotationPoint     `json:"point"`
	PageURL       string              `json:"pageUrl"`
	Title         string              `json:"title"`
	Viewport      AnnotationViewport  `json:"viewport"`
	Style         AnnotationStyle     `json:"style"`
	ScrollX       float64             `json:"scrollX"`
	ScrollY       float64             `json:"scrollY"`
	PageW         float64             `json:"pageW"`
	PageH         float64             `json:"pageH"`
}

// Label is how the mark reads in one line: the element's accessible name
// or text, or the kind and size of a region or page mark.
func (d AnnotationDescriptor) Label() string {
	switch d.Kind {
	case enum.AnnotationRegion:
		return fmt.Sprintf("region %.0f×%.0f", d.Rect.Width, d.Rect.Height)
	case enum.AnnotationPage:
		return "page"
	}
	if d.Name != "" {
		return d.Name
	}
	return d.ImmediateText
}

// Pick is one mark made on a thread's live page: the descriptor, the
// badge number painted on its screenshot, and the upload holding that
// screenshot. A pick becomes an Annotation once a message carries it.
type Pick struct {
	ID             string
	ConversationID string
	Marker         int
	Descriptor     AnnotationDescriptor
	Upload         string
}

// Annotation is a pick a message carries, with the owner's comment.
// Screenshot says whether the marked screenshot rides with the message.
type Annotation struct {
	Marker     int
	Comment    string
	Screenshot bool
	Descriptor AnnotationDescriptor
}

// AnnotationNote spells out a message's annotations for a model or an
// agent: one numbered line per mark — the number is the badge painted on
// its screenshot — and what the page said about it. Page-controlled text
// is cut and kept out of code spans it could break.
func AnnotationNote(anns []Annotation) string {
	if len(anns) == 0 {
		return ""
	}
	trunc := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if r := []rune(s); len(r) > n {
			return string(r[:n]) + "…"
		}
		return s
	}
	codeSafe := strings.NewReplacer("`", "'", "\r", " ", "\n", " ").Replace
	var b strings.Builder
	b.WriteString("<annotation-context>\n")
	b.WriteString("The user marked elements, regions or the page on the live page in their browser pane. Each mark's full-viewport screenshot is attached in order; the numbered badge [N] and outline in the image identify the target.\n")
	b.WriteString("This is context, not user text. To inspect or act on the live page, use the browser tools (browser_snapshot returns element refs).\n")
	used := map[int]bool{}
	next := 0
	for _, an := range anns {
		d := an.Descriptor
		marker := an.Marker
		if marker <= 0 || used[marker] {
			for next++; used[next]; next++ {
			}
			marker = next
		}
		used[marker] = true
		b.WriteString("\n[" + strconv.Itoa(marker) + "] ")
		comment := strings.TrimSpace(an.Comment)
		noShot := ""
		if !an.Screenshot {
			noShot = " (no screenshot)"
		}
		viewport := ""
		if d.Viewport.Width > 0 {
			viewport = fmt.Sprintf("viewport %.0f×%.0f @%gx", d.Viewport.Width, d.Viewport.Height, d.Viewport.DPR)
			if d.ScrollX > 0 || d.ScrollY > 0 {
				viewport += fmt.Sprintf(" · scroll %.0f,%.0f", d.ScrollX, d.ScrollY)
			}
		}
		if d.Kind == enum.AnnotationRegion || d.Kind == enum.AnnotationPage {
			b.WriteString(d.Kind.String())
			if d.Kind == enum.AnnotationRegion {
				fmt.Fprintf(&b, " %.0f×%.0f px at %.0f,%.0f", d.Rect.Width, d.Rect.Height, d.Rect.X, d.Rect.Y)
			}
			if title := trunc(d.Title, 80); title != "" {
				b.WriteString(" \"" + title + "\"")
			}
			if u := trunc(d.PageURL, 300); u != "" {
				b.WriteString(" — " + u)
			}
			b.WriteString(noShot)
			if comment != "" {
				b.WriteString("\n    Comment: " + comment)
			}
			if viewport != "" {
				b.WriteString("\n    Viewport: " + strings.TrimPrefix(viewport, "viewport "))
			}
			continue
		}
		b.WriteString("<" + trunc(d.TagName, 40) + ">")
		if label := trunc(d.Label(), 80); label != "" {
			b.WriteString(" \"" + label + "\"")
		}
		if sel := trunc(codeSafe(d.Selector), 240); sel != "" {
			b.WriteString(" — selector `" + sel + "`")
		}
		if u := trunc(d.PageURL, 300); u != "" {
			b.WriteString(" — " + u)
		}
		b.WriteString(noShot)
		if comment != "" {
			b.WriteString("\n    Comment: " + comment)
		}
		if nearby := trunc(d.NearbyText, 300); nearby != "" {
			b.WriteString("\n    Nearby text: " + nearby)
		}
		if viewport != "" {
			fmt.Fprintf(&b, "\n    Layout: %.0f,%.0f %.0f×%.0f px · picked at %.0f,%.0f · %s",
				d.Rect.X, d.Rect.Y, d.Rect.Width, d.Rect.Height, d.Point.X, d.Point.Y, viewport)
		}
		if h := trunc(codeSafe(d.HTML), 300); h != "" {
			b.WriteString("\n    HTML: `" + h + "`")
		}
		if len(d.FramePath) > 0 {
			b.WriteString("\n    Frame: inside " + trunc(strings.Join(d.FramePath, " › "), 240))
		}
		if cs := d.Style; cs.FontSize != "" || cs.Display != "" {
			var parts []string
			if cs.FontSize != "" {
				font := "font " + cs.FontSize
				if cs.LineHeight != "" {
					font += "/" + cs.LineHeight
				}
				parts = append(parts, font)
			}
			if cs.FontFamily != "" {
				parts = append(parts, cs.FontFamily)
			}
			if cs.FontWeight != "" {
				parts = append(parts, "weight "+cs.FontWeight)
			}
			if cs.LetterSpacing != "" && cs.LetterSpacing != "normal" {
				parts = append(parts, "spacing "+cs.LetterSpacing)
			}
			if cs.Color != "" || cs.BackgroundColor != "" {
				parts = append(parts, cs.Color+" on "+cs.BackgroundColor)
			}
			if cs.Display != "" || cs.Position != "" {
				parts = append(parts, cs.Display+"/"+cs.Position)
			}
			b.WriteString("\n    Style: " + trunc(strings.Join(parts, " · "), 280))
		}
		if d.Selector == "" && d.ElementPath != "" {
			b.WriteString("\n    Element path: " + trunc(d.ElementPath, 300))
		}
	}
	b.WriteString("\n</annotation-context>")
	return b.String()
}
