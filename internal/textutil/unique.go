package textutil

import "strings"

// UniqueNonBlank returns the values that are non-empty after trimming,
// de-duplicated, first occurrence first. nil when nothing qualifies.
func UniqueNonBlank(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
