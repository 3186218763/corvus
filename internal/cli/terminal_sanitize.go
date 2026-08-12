package cli

import "strings"

// Terminal-control sanitization for transcript content. Text that reaches the
// terminal through the transcript renderer (user paste, model reasoning, tool
// stream output) must never be able to take over the display: an OSC 52
// sequence can silently rewrite the user's clipboard, a clear-screen can wipe
// the UI, and a cursor-hide can strand the session. Only SGR color sequences
// survive so legitimately colored output still renders.
func sanitizeTerminalText(s string) string {
	if !strings.ContainsRune(s, '\x1b') {
		return sanitizeC0(s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for s != "" {
		i := strings.IndexByte(s, '\x1b')
		if i < 0 {
			b.WriteString(sanitizeC0(s))
			break
		}
		b.WriteString(sanitizeC0(s[:i]))
		rest := s[i:]
		if sgrSequence(rest) {
			b.WriteString(rest[:sgrSequenceLen(rest)])
			s = rest[sgrSequenceLen(rest):]
			continue
		}
		s = rest[escapeSequenceLen(rest):]
	}
	return b.String()
}

// sgrSequence reports whether s starts with an SGR color sequence (CSI with
// parameter bytes and final byte 'm'), the only ESC sequence worth keeping.
func sgrSequence(s string) bool {
	return sgrSequenceLen(s) > 0
}

func sgrSequenceLen(s string) int {
	if len(s) < 3 || s[0] != '\x1b' || s[1] != '[' {
		return 0
	}
	i := 2
	for i < len(s) && s[i] >= '0' && s[i] <= '?' {
		i++
	}
	if i < len(s) && s[i] == 'm' {
		return i + 1
	}
	return 0
}

// escapeSequenceLen returns the full byte length of the ESC sequence starting
// at s[0], consuming the whole string for an unterminated one.
func escapeSequenceLen(s string) int {
	if len(s) == 0 || s[0] != '\x1b' {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	rest := s[1:]
	switch {
	case strings.HasPrefix(rest, "]"): // OSC: terminated by BEL or ST
		if j := strings.IndexByte(rest[1:], '\x07'); j >= 0 {
			return 2 + j
		}
		if j := strings.Index(rest[1:], "\x1b\\"); j >= 0 {
			return 2 + j + 2
		}
		return len(s)
	case strings.HasPrefix(rest, "["): // CSI: params + intermediates + final byte
		i := 1
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '?' {
			i++
		}
		for i < len(rest) && rest[i] >= ' ' && rest[i] <= '/' {
			i++
		}
		if i < len(rest) {
			return i + 2 // include ESC [ prefix
		}
		return len(s)
	default: // single-char escape (e.g. ESC c, ESC 7)
		return 2
	}
}

// sanitizeC0 drops C0 control bytes (keeping tab, newline, carriage return)
// and DEL, which otherwise corrupt terminal rendering or input state.
func sanitizeC0(s string) string {
	if !strings.ContainsAny(s, "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f\x7f") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t' && c != '\n' && c != '\r') || c == 0x7f {
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
