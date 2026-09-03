package interaction

// Runner picks the horizontal drag-run pose while a left-button hold moves the
// pet. Direction is settled from accumulated horizontal pointer motion with
// hysteresis, so the left/right running animations do not flap when the
// pointer jitters or moves vertically. It is pure logic, independent from SDL.
type Runner struct {
	step  int32 // pixels of horizontal motion needed to settle a direction
	dir   int   // -1 left, 0 unsettled, +1 right; sticky once settled
	accum int32
}

// NewRunner creates a drag-run direction tracker. A non-positive step uses
// eight pixels, small enough to settle a real drag promptly yet wide enough to
// absorb sensor jitter.
func NewRunner(step int32) *Runner {
	if step <= 0 {
		step = 8
	}
	return &Runner{step: step}
}

// Update feeds the latest horizontal pointer delta (positive = right) and
// returns the current settled direction: +1 right, -1 left, 0 none yet.
func (r *Runner) Update(dx int32) int {
	if r == nil {
		return 0
	}
	r.accum += dx
	switch {
	case r.accum >= r.step:
		r.dir = 1
		r.accum = 0
	case r.accum <= -r.step:
		r.dir = -1
		r.accum = 0
	}
	return r.dir
}

// Direction returns the settled direction without consuming motion.
func (r *Runner) Direction() int {
	if r == nil {
		return 0
	}
	return r.dir
}

// Reset clears the direction so a fresh drag starts from an unsettled pose.
func (r *Runner) Reset() {
	if r == nil {
		return
	}
	r.dir = 0
	r.accum = 0
}
