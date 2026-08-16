package textutil

import "strings"

// FirstNonBlank returns the first value that is non-empty after trimming,
// with surrounding whitespace removed. "" if every value is blank.
func FirstNonBlank(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
