package turndiff

import (
	"fmt"
	"strings"
)

const (
	unifiedContext = 3
	// maxDPCells skips LCS when the DP table would exceed this many cells
	// (pathological rewrites fall back to a coarse exact replace).
	maxDPCells = 2_000_000
)

func splitDiffLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if strings.HasSuffix(s, "\n") {
		return lines[:len(lines)-1]
	}
	return lines
}

func unifiedBody(oldContent, newContent string) string {
	oldLines := splitDiffLines(oldContent)
	newLines := splitDiffLines(newContent)
	n, m := len(oldLines), len(newLines)
	if n == 0 && m == 0 {
		return ""
	}
	if int64(n)*int64(m) > maxDPCells {
		return coarseUnified(oldLines, newLines)
	}
	ops := lcsOps(oldLines, newLines)
	return emitHunks(ops)
}

func coarseUnified(oldLines, newLines []string) string {
	var b strings.Builder
	b.WriteString(hunkHeader(oldCountStart(len(oldLines)), len(oldLines), newCountStart(len(newLines)), len(newLines)))
	for _, line := range oldLines {
		b.WriteByte('-')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range newLines {
		b.WriteByte('+')
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func oldCountStart(count int) int {
	if count == 0 {
		return 0
	}
	return 1
}

func newCountStart(count int) int {
	if count == 0 {
		return 0
	}
	return 1
}

func hunkHeader(oldStart, oldCount, newStart, newCount int) string {
	return fmt.Sprintf("@@ -%s +%s @@\n", formatRange(oldStart, oldCount), formatRange(newStart, newCount))
}

func formatRange(start, count int) string {
	if count == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

type opKind int

const (
	opKeep opKind = iota
	opDel
	opIns
)

type diffOp struct {
	kind     opKind
	line     string
	oldIndex int // 1-based file line, 0 if not from old
	newIndex int // 1-based file line, 0 if not from new
}

func lcsOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{kind: opKeep, line: a[i], oldIndex: i + 1, newIndex: j + 1})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffOp{kind: opDel, line: a[i], oldIndex: i + 1})
			i++
		} else {
			ops = append(ops, diffOp{kind: opIns, line: b[j], newIndex: j + 1})
			j++
		}
	}
	for i < n {
		ops = append(ops, diffOp{kind: opDel, line: a[i], oldIndex: i + 1})
		i++
	}
	for j < m {
		ops = append(ops, diffOp{kind: opIns, line: b[j], newIndex: j + 1})
		j++
	}
	return ops
}

func emitHunks(ops []diffOp) string {
	if len(ops) == 0 {
		return ""
	}
	changed := make([]bool, len(ops))
	any := false
	for i, op := range ops {
		if op.kind != opKeep {
			changed[i] = true
			any = true
		}
	}
	if !any {
		return ""
	}

	var b strings.Builder
	i := 0
	for i < len(ops) {
		for i < len(ops) && !changed[i] {
			i++
		}
		if i >= len(ops) {
			break
		}
		start := i - unifiedContext
		if start < 0 {
			start = 0
		}
		end := i + 1
		for end < len(ops) {
			if changed[end] {
				end++
				continue
			}
			run := 0
			j := end
			for j < len(ops) && !changed[j] {
				run++
				j++
			}
			if j == len(ops) {
				break
			}
			if run <= unifiedContext*2 {
				end = j
				continue
			}
			break
		}
		lastChange := start
		for k := start; k < end && k < len(ops); k++ {
			if changed[k] {
				lastChange = k
			}
		}
		hunkEnd := lastChange + 1 + unifiedContext
		if hunkEnd > len(ops) {
			hunkEnd = len(ops)
		}

		oldStart, newStart := hunkLineStarts(ops, start)
		oldCount, newCount := 0, 0
		var body strings.Builder
		for k := start; k < hunkEnd; k++ {
			op := ops[k]
			switch op.kind {
			case opKeep:
				oldCount++
				newCount++
				body.WriteByte(' ')
			case opDel:
				oldCount++
				body.WriteByte('-')
			case opIns:
				newCount++
				body.WriteByte('+')
			}
			body.WriteString(op.line)
			body.WriteByte('\n')
		}
		if oldCount == 0 {
			oldStart = emptyHunkStart(ops, start, true)
		}
		if newCount == 0 {
			newStart = emptyHunkStart(ops, start, false)
		}
		b.WriteString(hunkHeader(oldStart, oldCount, newStart, newCount))
		b.WriteString(body.String())
		i = hunkEnd
	}
	return b.String()
}

func hunkLineStarts(ops []diffOp, start int) (oldStart, newStart int) {
	oldStart, newStart = 0, 0
	for k := start; k < len(ops); k++ {
		if oldStart == 0 && ops[k].oldIndex > 0 {
			oldStart = ops[k].oldIndex
		}
		if newStart == 0 && ops[k].newIndex > 0 {
			newStart = ops[k].newIndex
		}
		if (oldStart > 0 || !opTouchesOld(ops[k])) && (newStart > 0 || !opTouchesNew(ops[k])) {
			if oldStart > 0 && newStart > 0 {
				break
			}
		}
	}
	if oldStart == 0 {
		oldStart = 1
	}
	if newStart == 0 {
		newStart = 1
	}
	return oldStart, newStart
}

func opTouchesOld(op diffOp) bool { return op.kind == opKeep || op.kind == opDel }
func opTouchesNew(op diffOp) bool { return op.kind == opKeep || op.kind == opIns }

func emptyHunkStart(ops []diffOp, start int, old bool) int {
	for k := start - 1; k >= 0; k-- {
		if old && ops[k].oldIndex > 0 {
			return ops[k].oldIndex
		}
		if !old && ops[k].newIndex > 0 {
			return ops[k].newIndex
		}
	}
	return 0
}
