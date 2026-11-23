package enum

// AnnotationKind names what the owner marked on the live page.
type AnnotationKind string

const (
	// AnnotationElement is one element, hit-tested at a point.
	AnnotationElement AnnotationKind = "element"
	// AnnotationRegion is a rectangle the owner drew.
	AnnotationRegion AnnotationKind = "region"
	// AnnotationPage is the whole viewport.
	AnnotationPage AnnotationKind = "page"
)

func (k AnnotationKind) String() string { return string(k) }

func ParseAnnotationKind(v string) (AnnotationKind, bool) {
	switch k := AnnotationKind(v); k {
	case AnnotationElement, AnnotationRegion, AnnotationPage:
		return k, true
	}
	return "", false
}
