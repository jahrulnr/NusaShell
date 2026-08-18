package domain

// AsciiCount returns the number of ASCII (single-byte) bytes in s. It is a
// pure helper used by token-estimation heuristics that surcharge non-ASCII
// (e.g. CJK) characters.
func AsciiCount(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < 0x80 {
			n++
		}
	}
	return n
}
