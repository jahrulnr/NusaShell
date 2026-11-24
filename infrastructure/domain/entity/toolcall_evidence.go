package entity

// untrustedTagOverhead is the fixed byte length of the evidence envelope
// OutputWithUntrustedTag wraps around bounded output — the opening tag
// with an empty latency attribute, both newlines, and the closing tag.
const untrustedTagOverhead = len("<untrusted_tool latency=\"\">\n") + len("\n</untrusted_tool>")

// OutputWithUntrustedTagLen reports len(OutputWithUntrustedTag()) without
// building the tagged string. EstimatedTokens runs it per call per
// request, and materializing a copy of every tool output just to measure
// it was several MB of garbage per turn on tool-heavy histories.
//
// The envelope's delimiter neutralization substitutes one-for-one
// ("<untrusted_tool" → "<untrusted tool"), so it never changes the length
// and needs no accounting here.
func (t ToolCall) OutputWithUntrustedTagLen() int {
	if !t.outputSet {
		return 0
	}
	return t.outputWithMiddleTruncateLen() + untrustedTagOverhead + len(t.outputLatency)
}

// outputWithMiddleTruncateLen mirrors OutputWithMiddleTruncate's byte math
// on lengths: the head/tail getters only slice the stored output (no
// copy), and the omitted-count marker's length is arithmetic, so the
// result size is computable without materializing the string.
func (t ToolCall) outputWithMiddleTruncateLen() int {
	if !t.NeedTruncate() {
		return len(t.output)
	}
	budget := max(0, t.outputBudget)
	total := len(t.output)
	room := budget - omittedMarkerLen(total)
	if room <= 0 {
		return len(outputHead(t.output, budget))
	}
	headLen := len(outputHead(t.output, room*3/5))
	tailLen := len(outputTail(t.output, room-headLen))
	return headLen + omittedMarkerLen(max(0, total-headLen-tailLen)) + tailLen
}

// omittedMarkerLen is len(fmt.Sprintf(markerFmt, omitted, "")) for the
// marker OutputWithMiddleTruncateWindow splices into a truncated result —
// the format is mirrored from toolcall.go, and the equality test in
// toolcall_evidence_test.go fails if they drift apart.
func omittedMarkerLen(omitted int) int {
	const markerFmt = "\n\n[... %d bytes omitted%s ...]\n\n"
	return len(markerFmt) - len("%d%s") + decimalLen(omitted)
}

// decimalLen is the digit count %d prints for a non-negative n.
func decimalLen(n int) int {
	d := 1
	for n >= 10 {
		n /= 10
		d++
	}
	return d
}
