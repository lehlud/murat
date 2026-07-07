package textutil

import (
	"strings"
	"unicode"
)

// CleanHeaderValue returns a safe single-line value for RFC822 headers and
// terminal list fields derived from message metadata.
func CleanHeaderValue(value string) string {
	if value == "" {
		return ""
	}
	mapped := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == 0x7f {
			return ' '
		}
		return r
	}, value)
	return strings.Join(strings.Fields(mapped), " ")
}
