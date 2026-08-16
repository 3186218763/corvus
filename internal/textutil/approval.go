package textutil

import "strings"

// ApprovalPrefixNegated reports whether the text immediately before a matched
// approval phrase negates it, so plans that explicitly rule out an approval
// round do not trigger a needless one. windowBytes bounds how much of the
// prefix counts (0 = the whole prefix); suffix matches the negation terms only
// at the end of the trimmed prefix instead of anywhere inside it.
func ApprovalPrefixNegated(prefix string, windowBytes int, suffix bool, negations []string) bool {
	if windowBytes > 0 && len(prefix) > windowBytes {
		prefix = prefix[len(prefix)-windowBytes:]
	}
	if suffix {
		prefix = strings.TrimSpace(prefix)
		for _, negation := range negations {
			if strings.HasSuffix(prefix, negation) {
				return true
			}
		}
		return false
	}
	for _, negation := range negations {
		if strings.Contains(prefix, negation) {
			return true
		}
	}
	return false
}

// ContainsNonASCII reports whether s carries any rune outside ASCII.
func ContainsNonASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return true
		}
	}
	return false
}
