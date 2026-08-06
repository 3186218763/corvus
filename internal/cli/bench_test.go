package cli

import (
	"fmt"
	"strings"
	"testing"
)

// Research benchmarks for the render-animation audit (2026-08-06).
// Quantify per-update re-wrap cost of the whole transcript.

func benchTranscriptContent(lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch i % 4 {
		case 0:
			b.WriteString("\x1b[2m  ⎿  " + strings.Repeat("tool output line", 4) + "\x1b[0m\n")
		case 1:
			b.WriteString("\x1b[38;5;173m● Tool(verb)\x1b[0m\n")
		case 2:
			fmt.Fprintf(&b, "普通文本行 %d：中文内容用于宽度与换行测试\x1b[0m\n", i)
		case 3:
			b.WriteString("\x1b[38;5;245m· \x1b[0m\x1b[38;5;252m" + strings.Repeat("assistant text ", 8) + "\x1b[0m\n")
		}
	}
	return b.String()
}

func BenchmarkWrapTranscript(b *testing.B) {
	for _, n := range []int{500, 2000, 5000, 10000} {
		content := benchTranscriptContent(n)
		for _, width := range []int{80, 120} {
			b.Run(fmt.Sprintf("lines=%d/width=%d", n, width), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					wrapTranscript(content, width)
				}
			})
		}
	}
}
