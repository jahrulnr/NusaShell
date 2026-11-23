package telegram

import (
	"unicode/utf16"
)

// truncateUTF16 cuts s to at most maxUnits UTF-16 code units, never splitting
// a rune. Telegram counts message and caption length in UTF-16, so a rune
// count lets astral emoji (two units each) exceed the limit after the source
// text passed the rune cap.
func truncateUTF16(s string, maxUnits int) string {
	if maxUnits <= 0 || utf16Units(s) <= maxUnits {
		return s
	}
	units := 0
	for i, r := range s {
		n := max(utf16.RuneLen(r), 1)
		if units+n > maxUnits {
			return s[:i]
		}
		units += n
	}
	return s
}

func utf16Units(s string) int {
	n := 0
	for _, r := range s {
		u := max(utf16.RuneLen(r), 1)
		n += u
	}
	return n
}
