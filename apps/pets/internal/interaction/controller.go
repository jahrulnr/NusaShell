// Package interaction contains the desktop pet's pointer interaction policy.
// It is independent from SDL so click-versus-drag behavior is deterministic
// and unit-testable.
package interaction

import "time"

// Point is a desktop or window-local integer coordinate.
type Point struct {
	X int32
	Y int32
}

// Controller distinguishes a click from a drag using a small movement
// threshold. A click is returned by Release; a drag only updates window
// positions and never activates the pet.
type Controller struct {
	threshold int32
	pressed   bool
	dragged   bool
	pressAt   Point
	windowAt  Point
}

// NewController creates a controller. A non-positive threshold uses five
// pixels, which prevents tiny pointer jitter from becoming a drag.
func NewController(threshold int32) *Controller {
	if threshold <= 0 {
		threshold = 5
	}
	return &Controller{threshold: threshold}
}

// Press begins a possible drag at cursor and records the window origin.
func (c *Controller) Press(cursor, windowOrigin Point) {
	c.pressed = true
	c.dragged = false
	c.pressAt = cursor
	c.windowAt = windowOrigin
}

// Move returns the new window origin and whether the press has crossed the
// drag threshold. The origin is unchanged until the threshold is crossed.
func (c *Controller) Move(cursor Point) (Point, bool) {
	if c == nil || !c.pressed {
		return Point{}, false
	}
	dx := cursor.X - c.pressAt.X
	dy := cursor.Y - c.pressAt.Y
	if !c.dragged && (abs(dx) >= c.threshold || abs(dy) >= c.threshold) {
		c.dragged = true
	}
	if !c.dragged {
		return c.windowAt, false
	}
	return Point{X: c.windowAt.X + dx, Y: c.windowAt.Y + dy}, true
}

// MoveWhileHeld advances a drag from a polled desktop pointer. A released
// button cancels the gesture instead of activating the pet because the native
// button-release event may have been delivered outside the shaped window.
func (c *Controller) MoveWhileHeld(cursor Point, leftHeld bool) (Point, bool) {
	if !leftHeld {
		c.Cancel()
		return Point{}, false
	}
	return c.Move(cursor)
}

// PollInterval returns the cadence used to follow a held pointer. SDL reports
// a display refresh rate as zero when it is unspecified, so use 60 Hz only in
// that case.
func PollInterval(refreshRate int) time.Duration {
	if refreshRate <= 0 {
		refreshRate = 60
	}
	return time.Second / time.Duration(refreshRate)
}

// Release finishes the gesture and reports whether it was a click.
func (c *Controller) Release(_ Point) bool {
	if c == nil || !c.pressed {
		return false
	}
	activate := !c.dragged
	c.reset()
	return activate
}

// Cancel discards the current gesture without activating the pet.
func (c *Controller) Cancel() {
	if c != nil {
		c.reset()
	}
}

func (c *Controller) reset() {
	c.pressed = false
	c.dragged = false
	c.pressAt = Point{}
	c.windowAt = Point{}
}

func abs(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
