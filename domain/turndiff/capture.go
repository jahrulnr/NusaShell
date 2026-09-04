package turndiff

import "context"

type captureKey struct{}

// Capture holds the delta recorded by one file tool invocation.
type Capture struct {
	Delta Delta
	set   bool
}

// WithCapture stores a Capture on ctx so file tools can record a delta
// without changing their result type.
func WithCapture(ctx context.Context) (context.Context, *Capture) {
	if ctx == nil {
		ctx = context.Background()
	}
	c := &Capture{}
	return context.WithValue(ctx, captureKey{}, c), c
}

// Record stores d on the Capture attached to ctx, if any.
func Record(ctx context.Context, d Delta) {
	c := captureFrom(ctx)
	if c == nil {
		return
	}
	c.Delta = d
	c.set = true
}

// Take returns the recorded delta, or false when nothing was recorded.
func (c *Capture) Take() (Delta, bool) {
	if c == nil || !c.set {
		return Delta{}, false
	}
	return c.Delta, true
}

func captureFrom(ctx context.Context) *Capture {
	if ctx == nil {
		return nil
	}
	c, _ := ctx.Value(captureKey{}).(*Capture)
	return c
}
