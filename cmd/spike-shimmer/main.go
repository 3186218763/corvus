package main

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// A/B spike: working line with a single-pass shimmer sweep vs static.
// Run: go run ./cmd/spike-shimmer   (see both variants for 3s each)
func main() {
	stops := []color.Color{lipgloss.Color("#858b96"), lipgloss.Color("#d97757")}
	ramp := lipgloss.Blend1D(24, stops...)
	text := "⠋ thinking… ( 12s · Esc cancels)"
	for round := 0; round < 2; round++ {
		for i := 0; i < 120; i++ { // 3s at ~40fps
			var line string
			if round == 0 {
				pos := i % len(ramp)
				line = lipgloss.NewStyle().Foreground(ramp[pos]).Render(text)
			} else {
				line = text
			}
			clear := strings.Repeat("\r", len([]rune(line))) + "\x1b[K"
			fmt.Print(clear + line)
			time.Sleep(25 * time.Millisecond)
		}
		fmt.Println("\n--- round", round, "done ---")
		time.Sleep(time.Second)
	}
}
