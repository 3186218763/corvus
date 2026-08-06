package cli

import (
	"time"

	"corvus/internal/control"
	"corvus/internal/memory"
)

func renderMemory(width int, set *memory.Set) string {
	return viewProtectLines(control.RenderMemorySummary(set, time.Now().UTC()), width)
}
