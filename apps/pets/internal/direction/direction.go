// Package direction maps a pointer position inside the pet window to the
// hatch-pet v2 clockwise look-direction index.
package direction

import "math"

// Neutral is the front-facing/dead-zone value. The renderer uses the normal
// idle animation instead of a look-direction cell for this value.
const Neutral = -1

const directions = 16

// IndexFromPoint returns the nearest 22.5-degree look direction. Direction 0
// is screen-up, 4 is screen-right, 8 is screen-down, and 12 is screen-left.
// Coordinates are window-local, with (0,0) at the top-left.
func IndexFromPoint(x, y, width, height int32, deadzone float64) int {
	if width <= 0 || height <= 0 {
		return Neutral
	}
	cx := float64(width) / 2
	cy := float64(height) / 2
	dx := float64(x) - cx
	dy := float64(y) - cy
	if deadzone < 0 {
		deadzone = 0
	}
	if math.Hypot(dx, dy) <= deadzone {
		return Neutral
	}
	angle := math.Atan2(dx, -dy) * 180 / math.Pi
	if angle < 0 {
		angle += 360
	}
	index := int(math.Floor((angle+11.25)/22.5)) % directions
	return index
}
