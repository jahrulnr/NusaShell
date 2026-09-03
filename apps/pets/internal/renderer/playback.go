package renderer

import "time"

// slowPlaybackDelay applies the pet's deliberately relaxed playback rate.
// Animation delays are authored for the atlas; doubling them makes the
// rendered animation run at 0.5x without changing the asset or its state
// semantics.
func slowPlaybackDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 10 * time.Millisecond
	}
	return delay * 2
}

// nextFrame advances a zero-based animation frame. Non-looping animations
// report the last frame as finished instead of wrapping, so the caller can
// return to an idle state after the final frame has been displayed.
func nextFrame(current, frameCount int, loop bool) (next int, finished bool) {
	if frameCount <= 0 {
		return 0, true
	}
	if current < 0 || current >= frameCount {
		current = 0
	}
	if current+1 < frameCount {
		return current + 1, false
	}
	if loop {
		return 0, false
	}
	return current, true
}
